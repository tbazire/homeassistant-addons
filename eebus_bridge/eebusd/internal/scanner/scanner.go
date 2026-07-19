// Package scanner implements the generic, use-case-agnostic data scan performed
// against a remote EEBUS/SPINE device once SHIP pairing is established.
//
// Two complementary mechanisms are used:
//   - Typed use cases (MGCP, MPC, VABD, VAPD...) which interpret specific
//     measurements semantically (e.g. "grid connection point power"). See usecases.go.
//   - Generic features/client helpers (Measurement, DeviceClassification,
//     DeviceConfiguration...) which enumerate everything the remote exposes,
//     regardless of its entity type. See scanner.go.
//
// Together they give a "scan anything" client that works whether or not the
// remote's role matches one of the known use cases.
package scanner

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/enbility/eebus-go/features/client"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

// Options controls the scanner output format and refresh behavior.
type Options struct {
	// JSONOut, when true, emits measurement data as compact JSON lines
	// (machine-friendly). Otherwise a human-readable table is printed.
	JSONOut bool

	// PollInterval caps how often the scanner proactively re-issues SPINE read
	// requests (RequestData/RequestDescriptions/...) per remote entity. Values
	// pushed by the device via subscriptions are still displayed immediately;
	// only our own pull is throttled. This avoids the amplification loop
	// RequestData -> DataChange -> RequestEntityData -> RequestData.
	// 0 means subscription-only (never re-pull proactively).
	PollInterval time.Duration
}

// Scanner walks a remote SPINE device and pulls/prints the data it exposes.
// One Scanner instance is reused across all remote devices; per-device state
// is held in remoteTrackers keyed by SKI.
type Scanner struct {
	options Options

	// localEntity is the CEM entity helpers are bound against.
	localEntity spineapi.EntityLocalInterface

	// dataOut is where NDJSON lines are written in -json mode. Defaults to
	// os.Stdout; overridable via SetDataOut (mainly for tests).
	dataOut io.Writer

	mu       sync.Mutex
	trackers map[string]*remoteTracker // keyed by remote SKI
}

// remoteTracker holds per-remote-device scan state so we can refresh data
// later (e.g. on a notification) without re-creating the helpers.
type remoteTracker struct {
	ski             string
	device          spineapi.DeviceRemoteInterface
	measurements    map[string]*client.Measurement // keyed by entity address
	classifications map[string]*client.DeviceClassification
	configurations  map[string]*client.DeviceConfiguration
	scannedEntities map[string]struct{} // entity addresses already scanned (idempotency)

	// failedRequests caches "operation not supported" results per (entity, function)
	// so the periodic poller does not hammer an unsupported function on every
	// tick. Keyed by "addr/functionName".
	mu             sync.Mutex
	failedRequests map[string]bool
}

// NewScanner creates a Scanner bound to the provided local entity.
//
// It also ensures the local entity has the client-role features required by
// the features/client helpers (DeviceClassification, DeviceConfiguration,
// DeviceDiagnosis, Measurement, ElectricalConnection). Without these, the
// helpers return "local feature not found" because getLocalAndRemoteFeatures
// requires a matching local client feature for each remote server feature it
// wants to talk to. GetOrAddFeature is idempotent, so this is safe to call
// regardless of what the service/use-case registration already created.
func NewScanner(localEntity spineapi.EntityLocalInterface, options Options) *Scanner {
	if localEntity != nil {
		ensureLocalClientFeatures(localEntity)
	}
	return &Scanner{
		options:     options,
		localEntity: localEntity,
		trackers:    make(map[string]*remoteTracker),
	}
}

// requiredClientFeatures is the set of SPINE feature types the scanner wants
// to consume as a client (i.e. read from a remote server feature of the same
// type). Order does not matter; GetOrAddFeature is idempotent.
var requiredClientFeatures = []model.FeatureTypeType{
	model.FeatureTypeTypeDeviceClassification,
	model.FeatureTypeTypeDeviceConfiguration,
	model.FeatureTypeTypeDeviceDiagnosis,
	model.FeatureTypeTypeMeasurement,
	model.FeatureTypeTypeElectricalConnection,
}

// ensureLocalClientFeatures adds each required feature type to the local
// entity with the Client role, if it is not already present.
func ensureLocalClientFeatures(localEntity spineapi.EntityLocalInterface) {
	for _, ft := range requiredClientFeatures {
		if localEntity.FeatureOfTypeAndRole(ft, model.RoleTypeClient) == nil {
			localEntity.GetOrAddFeature(ft, model.RoleTypeClient)
			logDebugf("added local client feature: %s", ft)
		}
	}
}

// ScanRemoteDevice walks every entity of the remote device and triggers the
// appropriate feature requests. It is safe to call from a connection handler.
func (s *Scanner) ScanRemoteDevice(device spineapi.DeviceRemoteInterface) {
	if device == nil {
		return
	}
	ski := device.Ski()
	logInfof("=== scanning remote device SKI=%s ===", ski)

	s.getOrCreateTracker(device)

	for _, entity := range device.Entities() {
		s.ScanEntity(device, entity)
	}
	logInfof("=== scan complete for SKI=%s ===", ski)
}

// ScanEntity scans a single remote entity. It is idempotent per (ski, entity
// address): re-scanning an entity that was already scanned is a no-op (the
// per-entity tracker entry is already present). This is the entry point used
// when SPINE fires an EventTypeEntityChange + ElementChangeAdd event for a
// newly discovered entity, which is the reliable discovery hook (much better
// than racing the discovery in RemoteServiceConnected).
func (s *Scanner) ScanEntity(device spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface) {
	if device == nil || entity == nil {
		return
	}
	tracker := s.getOrCreateTracker(device)
	addr := entityAddressString(entity)

	// Idempotency: skip if we already scanned this entity. Re-scan would
	// re-create the helpers and re-request data pointlessly.
	s.mu.Lock()
	if _, ok := tracker.scannedEntities[addr]; ok {
		s.mu.Unlock()
		return
	}
	tracker.scannedEntities[addr] = struct{}{}
	s.mu.Unlock()

	s.scanEntity(tracker, entity)
}

// scanEntity prepares the feature helpers for a single remote entity.
//
// It only *creates* the features/client helpers and records them in the
// tracker; it does NOT issue Request* calls. The Request* calls need the
// remote's "possible operations" to be resolved first (they arrive via
// NodeManagementDetailedDiscoveryData, AFTER the EntityChange event). Calling
// Request* here would hit "operation is not supported on function" because
// remote operations are still unknown at this point.
//
// Actual data requests are issued from RequestEntityData, which is triggered
// later (on EventTypeDataChange / subscription completion / a short delay).
func (s *Scanner) scanEntity(tracker *remoteTracker, entity spineapi.EntityRemoteInterface) {
	if entity == nil {
		return
	}
	addr := entityAddressString(entity)
	entType := entity.EntityType()
	logInfof("entity %s type=%s", addr, entType)

	// 1. Device classification — manufacturer / model / serial.
	if dc, err := client.NewDeviceClassification(s.localEntity, entity); err == nil && dc != nil {
		tracker.classifications[addr] = dc
	} else {
		logDebugf("entity %s: no DeviceClassification server feature (%v)", addr, err)
	}

	// 2. Device configuration — key/value parameters (nominal peak power, etc.).
	if dc, err := client.NewDeviceConfiguration(s.localEntity, entity); err == nil && dc != nil {
		tracker.configurations[addr] = dc
	} else {
		logDebugf("entity %s: no DeviceConfiguration server feature (%v)", addr, err)
	}

	// 3. Measurement — the central data source for power/energy/current/...
	if m, err := client.NewMeasurement(s.localEntity, entity); err == nil && m != nil {
		tracker.measurements[addr] = m
	} else {
		logDebugf("entity %s: no Measurement server feature (%v)", addr, err)
	}

	// DeviceDiagnosis is created lazily when needed (RequestState path).
}

// RequestEntityData refreshes an entity's data: it issues SPINE read requests
// for every feature helper attached to the entity, then renders the cached
// values. It is the entry point used by the periodic poller (one ticker per
// entity, firing every Options.PollInterval) and by the initial pull right
// after entity discovery.
//
// It is NOT called from the SPINE DataChange event handler anymore: doing so
// caused an amplification loop (RequestData -> response -> DataChange ->
// RequestEntityData -> RequestData -> ...). DataChange now calls
// RenderEntityData, which only reprints cached values — pushed values are
// therefore displayed immediately, but never re-trigger a pull.
//
// Safe to call multiple times: per-function requests that previously returned
// "operation is not supported" are cached and skipped on subsequent calls.
func (s *Scanner) RequestEntityData(device spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface) {
	if device == nil || entity == nil {
		return
	}
	tracker := s.getOrCreateTracker(device)
	addr := entityAddressString(entity)
	s.pullEntityData(addr, entity, tracker)
	s.printEntityData(addr, entity)
}

// RenderEntityData only reprints the cached values for an entity, without
// issuing any SPINE request. It is the entry point for the DataChange event
// handler: when the device pushes new values (via a subscription), we want to
// display them immediately, but we must NOT re-pull (that would re-trigger
// DataChange and start the amplification loop).
func (s *Scanner) RenderEntityData(device spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface) {
	if device == nil || entity == nil {
		return
	}
	// Ensure a tracker exists so render finds the helpers. If the entity was
	// never scanned (no helpers), this is a no-op.
	addr := entityAddressString(entity)
	s.printEntityData(addr, entity)
}

// pullEntityData issues the SPINE read requests for one entity.
func (s *Scanner) pullEntityData(addr string, entity spineapi.EntityRemoteInterface, tracker *remoteTracker) {
	// Helper: run a request only if it has not previously failed as "not
	// supported" for this (entity, function). Cache failures.
	tryRequest := func(functionName string, do func() error) {
		if tracker.isFailed(addr, functionName) {
			return
		}
		if err := do(); err != nil {
			if strings.Contains(err.Error(), "operation is not supported") {
				tracker.markFailed(addr, functionName)
			} else {
				logDebugf("entity %s: %s: %v", addr, functionName, err)
			}
		}
	}

	// 1. Device classification.
	if dc := tracker.classifications[addr]; dc != nil {
		tryRequest("RequestManufacturerDetails", func() error {
			_, err := dc.RequestManufacturerDetails()
			return err
		})
	}

	// 2. Device configuration.
	if dc := tracker.configurations[addr]; dc != nil {
		tryRequest("RequestKeyValueDescriptions", func() error {
			_, err := dc.RequestKeyValueDescriptions(nil, nil)
			return err
		})
	}

	// 3. Measurement — descriptions + subscribe + values.
	if m := tracker.measurements[addr]; m != nil {
		tryRequest("RequestDescriptions", func() error {
			_, err := m.RequestDescriptions(nil, nil)
			return err
		})
		// Subscribe so future updates arrive as notifications. The subscription
		// itself only needs to happen once per entity; it is idempotent and
		// cached-as-failed if unsupported, so re-calling on each poll is safe.
		tryRequest("Subscribe", func() error {
			_, err := m.Subscribe()
			return err
		})
		tryRequest("RequestData", func() error {
			_, err := m.RequestData(nil, nil)
			return err
		})
	}

	// 4. Device diagnosis — created lazily.
	if dd, err := client.NewDeviceDiagnosis(s.localEntity, entity); err == nil && dd != nil {
		tryRequest("RequestState", func() error {
			_, err := dd.RequestState()
			return err
		})
	}
}

// printEntityData renders the cached values for one entity. Used by both
// RequestEntityData (after a pull) and RenderEntityData (push notification).
// Delegates to renderEntity in export.go (the presentation layer).
func (s *Scanner) printEntityData(addr string, entity spineapi.EntityRemoteInterface) {
	tracker := s.trackerForEntity(addr)
	if tracker == nil {
		return
	}
	s.renderEntity(tracker, addr, entity)
}

// trackerForEntity returns the tracker holding the helpers for a given entity
// address, or nil if no tracker has scanned that address yet.
func (s *Scanner) trackerForEntity(addr string) *remoteTracker {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.trackers {
		if _, ok := t.scannedEntities[addr]; ok {
			return t
		}
	}
	return nil
}

// isFailed returns true if a previous call for (addr, function) failed with
// "operation is not supported". Used to avoid retrying unsupported calls.
func (t *remoteTracker) isFailed(addr, function string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.failedRequests[addr+"/"+function]
}

// markFailed records that (addr, function) is not supported by the remote.
func (t *remoteTracker) markFailed(addr, function string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failedRequests[addr+"/"+function] = true
	logDebugf("entity %s: %s not supported by remote — will skip on next attempts", addr, function)
}

// getOrCreateTracker returns the per-device tracker, creating it on first use.
func (s *Scanner) getOrCreateTracker(device spineapi.DeviceRemoteInterface) *remoteTracker {
	ski := device.Ski()
	s.mu.Lock()
	defer s.mu.Unlock()
	if tracker, ok := s.trackers[ski]; ok {
		return tracker
	}
	tracker := &remoteTracker{
		ski:             ski,
		device:          device,
		measurements:    make(map[string]*client.Measurement),
		classifications: make(map[string]*client.DeviceClassification),
		configurations:  make(map[string]*client.DeviceConfiguration),
		scannedEntities: make(map[string]struct{}),
		failedRequests:  make(map[string]bool),
	}
	s.trackers[ski] = tracker
	return tracker
}

// entityAddressString renders a SPINE entity address (e.g. "3.1") for logging
// and for use as the NDJSON "entity" field. Kept here (not in export.go)
// because it is state/identity manipulation, not presentation.
func entityAddressString(e spineapi.EntityRemoteInterface) string {
	if e == nil || e.Address() == nil {
		return "?"
	}
	parts := make([]string, 0, len(e.Address().Entity))
	for _, a := range e.Address().Entity {
		parts = append(parts, fmt.Sprintf("%d", a))
	}
	return strings.Join(parts, ".")
}
