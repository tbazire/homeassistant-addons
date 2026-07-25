package internal

import (
	"context"
	"fmt"
	"sync"
	"time"

	"eebusd/internal/scanner"
	"github.com/enbility/eebus-go/api"
	"github.com/enbility/eebus-go/service"
	shipapi "github.com/enbility/ship-go/api"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

// App wires together the eebus-go Service, the read-only use cases and the
// generic data Scanner. It implements api.ServiceReaderInterface to receive
// SHIP-level events (pairing, mDNS discovery, connection lifecycle).
//
// Lifecycle:
//
//	cfg, _ := NewApp(config, logger)
//	cfg.Setup()   // build service, register use cases, prepare scanner
//	cfg.Start()   // mDNS announce, start hub
//	... run ...
//	cfg.Shutdown()
type App struct {
	cfg    *Config
	logger *Logger

	service *service.Service
	scanner *scanner.Scanner

	// localSKI is our own SKI (derived from the certificate). Used to skip
	// ourselves in auto-discovery.
	localSKI string

	mu             sync.Mutex
	discovered     []shipapi.RemoteMdnsService // last mDNS scan
	pairingState   map[string]string           // SKI -> human-readable state
	registeredSKIs map[string]struct{}         // SKIs we already called RegisterRemoteService on

	// pollCtx/pollCancel drive the per-entity periodic pollers (one goroutine
	// per remote entity, started in HandleEvent on EntityChange+Add). Cancelling
	// pollCtx stops all of them at once on Shutdown. The cadence is
	// cfg.PollInterval; without these pollers a pull-only device (one that does
	// not push its measurements) would never refresh, because DataChange only
	// reprints cached values now (no more pull-on-data-change).
	pollCtx    context.Context
	pollCancel context.CancelFunc
}

// NewApp constructs the App from a parsed Config. It does not yet Setup/Start
// the underlying service — call Setup() next.
func NewApp(cfg *Config, logger *Logger) *App {
	return &App{
		cfg:            cfg,
		logger:         logger,
		pairingState:   make(map[string]string),
		registeredSKIs: make(map[string]struct{}),
	}
}

// Setup builds the TLS material, the eebus Configuration, the Service, wires
// the read-only use cases and the generic Scanner. Returns an error if any
// step fails; on success App.service is ready to Start().
func (a *App) Setup() error {
	// Per-entity periodic pollers context. Cancelled by Shutdown.
	a.pollCtx, a.pollCancel = context.WithCancel(context.Background())
	// 1. Certificate (load existing or generate + persist).
	certificate, certPath, keyPath, err := a.cfg.LoadOrGenerateCertificate()
	if err != nil {
		return fmt.Errorf("certificate: %w", err)
	}

	// 2. Optional pairing config (SHIP Pairing Service, listener mode).
	pairingConfig, err := a.cfg.BuildPairingConfig()
	if err != nil {
		return fmt.Errorf("pairing config: %w", err)
	}

	// 3. Ring buffer: required when pairingConfig is non-nil (Listener/Both),
	//    harmless otherwise. We always provide one so the user can switch modes
	//    without code changes.
	ringBuffer := NewFileRingBuffer(a.cfg.CertDir + "/ringbuffer.json")

	// 4. eebus Configuration: we present ourselves as a CEM (Customer Energy
	//    Management System). CEM is the most permissive local entity type —
	//    MGCP explicitly lists it as a valid remote entity, and every cem/*
	//    use case works against it.
	configuration, err := api.NewConfiguration(
		a.cfg.VendorCode,
		a.cfg.Brand,
		a.cfg.Model,
		a.cfg.Serial,
		[]shipapi.DeviceCategoryType{shipapi.DeviceCategoryTypeEnergyManagementSystem},
		model.DeviceTypeTypeEnergyManagementSystem,
		[]model.EntityTypeType{model.EntityTypeTypeCEM},
		int(a.cfg.Port),
		certificate,
		a.cfg.Heartbeat,
		pairingConfig,
		ringBuffer,
	)
	if err != nil {
		return fmt.Errorf("new configuration: %w", err)
	}
	configuration.SetAlternateIdentifier(a.cfg.Brand + "-" + a.cfg.Serial)

	// 5. Service creation.
	a.service = service.NewService(configuration, a)
	a.service.SetLogging(a.logger)

	if err := a.service.Setup(); err != nil {
		return fmt.Errorf("service setup: %w", err)
	}

	// NOTE: SetAutoAccept and UserIsAbleToApproveOrCancelPairingRequests must
	// be called *after* Setup(): they touch s.localService, which is only
	// initialized during Setup() (ship-go api/servicedetails.go).
	if a.cfg.AutoAccept {
		a.service.SetAutoAccept(true)
	}
	// Allow user-verification pairing flow (waiting for trust). Without this
	// the hub denies incoming pairing requests immediately.
	a.service.UserIsAbleToApproveOrCancelPairingRequests(true)

	// 6. Local CEM entity — the anchor for both use cases and the scanner.
	localEntity := a.service.LocalDevice().EntityForType(model.EntityTypeTypeCEM)
	if localEntity == nil {
		return fmt.Errorf("no local CEM entity found after setup")
	}

	// 7. Typed read-only use cases (MGCP, MPC, VABD, VAPD).
	if _, err := scanner.RegisterUseCases(a.service, localEntity); err != nil {
		return fmt.Errorf("register use cases: %w", err)
	}

	// 8. Generic feature-based scanner.
	a.scanner = scanner.NewScanner(localEntity, scanner.Options{
		JSONOut:      a.cfg.JSONOut,
		PollInterval: a.cfg.PollInterval,
	})

	// 9. Capture our own SKI so we can skip it in auto-discovery. The SKI is
	//    derived from the certificate during Setup() and exposed via the local
	//    service details.
	if details := a.service.LocalService(); details != nil {
		a.localSKI = details.SKI()
		AppLog.Infof("Local SKI:%s", a.localSKI)
	}

	// 10. Subscribe to SPINE events. This is the reliable hook point for
	//     discovering remote entities/features: NodeManagementDetailedDiscovery
	//     fires EntityTypeEntityChange events as the remote exposes its
	//     entities. Doing the scan in RemoteServiceConnected (as V1 did) races
	//     the discovery and misses entities added after the callback.
	if events := localEntity.Device().Events(); events != nil {
		if err := events.Subscribe(a); err != nil {
			AppLog.Warnf("subscribe SPINE events: %v", err)
		} else {
			AppLog.Infof("subscribed to SPINE events")
		}
	} else {
		AppLog.Warnf("SPINE events manager is nil — entity discovery will be incomplete")
	}

	// 11. Log identifying info the operator will need (SKI, fingerprint, files).
	if fp, err := a.service.GetLocalCertificateFingerprint(); err == nil {
		AppLog.Infof("local fingerprint: %s", fp)
	}
	AppLog.Infof("certificate files: %s / %s", certPath, keyPath)
	if qr, err := a.service.QRCodeText(); err == nil {
		AppLog.Infof("SHIP QR code:\n%s", qr)
	}

	return nil
}

// Start begins mDNS announcement and the SHIP hub. If a remote SKI was
// provided via -remoteski, it is registered for outgoing pairing.
func (a *App) Start() error {
	if a.cfg.RemoteSKI != "" {
		AppLog.Infof("registering remote SKI %s for pairing", a.cfg.RemoteSKI)
		a.service.RegisterRemoteService(shipapi.NewServiceIdentity(a.cfg.RemoteSKI, "", ""))
	}
	if err := a.service.Start(); err != nil {
		return fmt.Errorf("service start: %w", err)
	}
	AppLog.Infof("service started on port %d (CEM)", a.cfg.Port)
	return nil
}

// Shutdown stops the service gracefully (mDNS teardown included).
func (a *App) Shutdown() {
	// Stop the per-entity periodic pollers first so they don't fire against a
	// tearing-down service.
	if a.pollCancel != nil {
		a.pollCancel()
	}
	if a.service != nil {
		a.service.Shutdown()
		AppLog.Infof("service shut down")
	}
}

// Service exposes the underlying *service.Service (e.g. for QR/mDNS queries).
func (a *App) Service() *service.Service { return a.service }

// ============================================================================
// api.ServiceReaderInterface implementation
// ============================================================================

// RemoteServiceConnected is called once SHIP + SPINE are up with a remote.
// This is where the generic scan kicks off.
func (a *App) RemoteServiceConnected(_ api.ServiceInterface, identity shipapi.ServiceIdentity) {
	AppLog.Infof(">>> remote service CONNECTED: %s", identity.String())

	// Resolve the SPINE remote device. It may take a few ms after this callback
	// for the device to be registered; the SPINE layer handles detailed
	// discovery automatically. We look it up by SKI.
	rDevice := a.service.LocalDevice().RemoteDeviceForSki(identity.SKI)
	if rDevice == nil {
		AppLog.Warnf("connected callback: no remote device for SKI %s yet", identity.SKI)
		return
	}
	a.scanner.ScanRemoteDevice(rDevice)
}

func (a *App) RemoteServiceDisconnected(_ api.ServiceInterface, identity shipapi.ServiceIdentity) {
	AppLog.Infof("<<< remote service DISCONNECTED: %s", identity.String())
	a.mu.Lock()
	delete(a.pairingState, identity.SKI)
	a.mu.Unlock()
}

// VisibleRemoteMdnsServicesUpdated is called whenever the set of EEBUS
// services discovered on the local network changes.
//
// In auto-discovery mode (the default), every newly seen SKI is registered
// for pairing via RegisterRemoteService. The hub deduplicates, so calling it
// several times for the same SKI is safe. Pairing itself only proceeds if
// AllowWaitingForTrust returns true for that identity.
//
// We never auto-pair our own SKI (the hub already filters it, but we guard
// here too for clarity).
func (a *App) VisibleRemoteMdnsServicesUpdated(_ api.ServiceInterface, entries []shipapi.RemoteMdnsService) {
	a.mu.Lock()
	a.discovered = append([]shipapi.RemoteMdnsService(nil), entries...)
	registered := a.registeredSKIs
	a.mu.Unlock()

	AppLog.Infof("=== mDNS discovery: %d service(s) visible ===", len(entries))
	for i, e := range entries {
		AppLog.Infof("  [%d] ski=%s shipID=%s brand=%s model=%s type=%s",
			i, e.Ski, e.ShipID, e.Brand, e.Model, e.Type)

		// Skip our own announcement and already-registered SKIs.
		if e.Ski == "" || e.Ski == a.localSKI {
			continue
		}
		if _, ok := registered[e.Ski]; ok {
			continue
		}

		AppLog.Infof("auto-discovery: registering SKI %s (%s %s) for pairing",
			e.Ski, e.Brand, e.Model)
		identity := shipapi.NewServiceIdentity(e.Ski, "", "")
		a.service.RegisterRemoteService(identity)

		a.mu.Lock()
		a.registeredSKIs[e.Ski] = struct{}{}
		a.mu.Unlock()
	}
	if len(entries) == 0 {
		AppLog.Infof("(none yet — check mDNS / firewall / avahi-daemon)")
	}
}

func (a *App) ServiceUpdated(identity shipapi.ServiceIdentity) {
	AppLog.Debugf("service updated: %s", identity.String())
}

// ServicePairingDetailUpdate reports handshake progress. We watch for the
// "remote denied trust" terminal state to bail out cleanly.
func (a *App) ServicePairingDetailUpdate(identity shipapi.ServiceIdentity, detail *shipapi.ConnectionStateDetail) {
	if detail == nil {
		return
	}
	state := detail.State()
	human := stateString(state)
	a.mu.Lock()
	a.pairingState[identity.SKI] = human
	a.mu.Unlock()

	AppLog.Infof("pairing state [%s]: %s", identity.SKI, human)

	switch state {
	case shipapi.ConnectionStateRemoteDeniedTrust:
		AppLog.Errorf("remote %s denied trust — cancelling pairing", identity.SKI)
		a.service.CancelPairing(identity)
		a.service.UnregisterRemoteService(identity)
	case shipapi.ConnectionStateCompleted:
		AppLog.Infof("pairing COMPLETED with %s", identity.SKI)
	case shipapi.ConnectionStateError:
		if err := detail.Error(); err != nil {
			AppLog.Errorf("pairing error with %s: %v", identity.SKI, err)
		}
	}
}

// --- SHIP Pairing Service events (only relevant with -secret) ---------------

func (a *App) ServiceAutoTrusted(_ api.ServiceInterface, identity shipapi.ServiceIdentity) {
	AppLog.Infof("auto-trust successful: %s", identity.String())
}

func (a *App) ServiceAutoTrustFailed(_ api.ServiceInterface, identity shipapi.ServiceIdentity, reason error) {
	AppLog.Warnf("auto-trust FAILED for %s: %v", identity.String(), reason)
}

func (a *App) ServiceAutoTrustRemoved(_ api.ServiceInterface, identity shipapi.ServiceIdentity, reason string) {
	AppLog.Warnf("auto-trust REMOVED for %s: %s", identity.String(), reason)
}

// --- helpers ---------------------------------------------------------------

func stateString(s shipapi.ConnectionState) string {
	switch s {
	case shipapi.ConnectionStateNone:
		return "none"
	case shipapi.ConnectionStateQueued:
		return "queued"
	case shipapi.ConnectionStateInitiated:
		return "initiated"
	case shipapi.ConnectionStateReceivedPairingRequest:
		return "receivedPairingRequest"
	case shipapi.ConnectionStateInProgress:
		return "inProgress"
	case shipapi.ConnectionStateTrusted:
		return "trusted"
	case shipapi.ConnectionStatePin:
		return "pin"
	case shipapi.ConnectionStateCompleted:
		return "completed"
	case shipapi.ConnectionStateRemoteDeniedTrust:
		return "remoteDeniedTrust"
	case shipapi.ConnectionStateError:
		return "error"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}

// ============================================================================
// spineapi.EventHandlerInterface implementation
// ============================================================================

// HandleEvent is the SPINE-level event hook. It is THE reliable discovery
// mechanism: NodeManagementDetailedDiscovery emits EntityTypeEntityChange
// events as the remote device reveals its entities/features, which happens
// AFTER RemoteServiceConnected. Without this, V1 was racing the discovery and
// only ever saw the initial DeviceInformation entity.
//
// We react to:
//   - EntityChange + Add   → scan the newly appeared entity
//   - EntityChange + Remove → (future) clean up MQTT entities for it
//   - DataChange           → the remote pushed new values; the use case
//     callbacks handle the bulk of this, but we log it.
func (a *App) HandleEvent(payload spineapi.EventPayload) {
	AppLog.Debugf("HandleEvent: type=%v change=%v ski=%s function=%v entity=%v",
		payload.EventType, payload.ChangeType, payload.Ski, payload.Function, payload.Entity != nil)
	switch payload.EventType {
	case spineapi.EventTypeEntityChange:
		switch payload.ChangeType {
		case spineapi.ElementChangeAdd:
			if payload.Entity == nil || payload.Device == nil {
				return
			}
			addr := entityAddrString(payload.Entity)
			AppLog.Debugf("SPINE entity added: ski=%s entity=%s type=%s",
				payload.Ski, addr, payload.Entity.EntityType())
			// 1. Prepare the feature helpers immediately (does not issue
			//    requests, so it cannot fail on "operation not supported").
			a.scanner.ScanEntity(payload.Device, payload.Entity)
			// 2. Schedule the initial pull after a short delay: the remote's
			//    "possible operations" arrive as part of
			//    NodeManagementDetailedDiscoveryData, which completes shortly
			//    AFTER the EntityChange event. Requesting too early fails with
			//    "operation is not supported on function". 2s is a pragmatic
			//    upper bound observed on the SR920.
			// 3. Then start a periodic poller so values keep refreshing even
			//    for pull-only devices (the SR920 does not push measurements).
			//    The poller is per-entity and stops on Shutdown via pollCtx.
			a.startEntityPoller(payload.Device, payload.Entity, payload.Ski, addr)
		case spineapi.ElementChangeRemove:
			if payload.Entity == nil {
				return
			}
			AppLog.Debugf("SPINE entity removed: ski=%s entity=%s",
				payload.Ski, entityAddrString(payload.Entity))
			// No-op for V1 stdout mode; MQTT jalot will remove HA entities here.
			// The per-entity poller goroutine will exit when pollCtx is cancelled
			// at Shutdown (entity removal in the SPINE model does not currently
			// map 1:1 to a goroutine we can identify by addr alone).
		}
	case spineapi.EventTypeDataChange:
		// The remote pushed new data (e.g. via a subscription notification) or
		// a subscription completed. We ONLY render the cached values here — we
		// do NOT re-issue requests, otherwise we'd restart the amplification
		// loop (RequestData -> response -> DataChange -> RequestEntityData ->
		// RequestData -> ...). Periodic pulls are owned by the per-entity
		// poller started on EntityChange+Add.
		if payload.Entity != nil && payload.Device != nil {
			a.scanner.RenderEntityData(payload.Device, payload.Entity)
		}
	}
}

// startEntityPoller launches the periodic data poller for one remote entity.
// It performs an initial pull after a short delay (the remote needs a moment
// to announce its possible operations after the EntityChange event) and then
// ticks at cfg.PollInterval. If PollInterval <= 0, only the initial pull runs
// (subscription-only mode: subsequent refreshes rely entirely on DataChange).
//
// The goroutine exits when pollCtx is cancelled (Shutdown) or when pollCtx is
// done for any other reason.
func (a *App) startEntityPoller(device spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, ski, addr string) {
	interval := a.cfg.PollInterval
	if interval <= 0 {
		// Subscription-only: one delayed initial pull, no ticker.
		time.AfterFunc(2*time.Second, func() {
			AppLog.Debugf("initial RequestEntityData (one-shot): ski=%s entity=%s", ski, addr)
			a.scanner.RequestEntityData(device, entity)
		})
		return
	}

	go func() {
		// Initial delay before the first pull (same rationale as the one-shot
		// path: possible operations are not known yet at EntityChange time).
		select {
		case <-a.pollCtx.Done():
			return
		case <-time.After(2 * time.Second):
		}
		AppLog.Debugf("periodic poller start: ski=%s entity=%s interval=%s", ski, addr, interval)
		// First pull.
		a.scanner.RequestEntityData(device, entity)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-a.pollCtx.Done():
				AppLog.Debugf("periodic poller stop: ski=%s entity=%s", ski, addr)
				return
			case <-ticker.C:
				a.scanner.RequestEntityData(device, entity)
			}
		}
	}()
}

func entityAddrString(e spineapi.EntityRemoteInterface) string {
	if e == nil || e.Address() == nil {
		return "?"
	}
	parts := make([]string, 0, len(e.Address().Entity))
	for _, a := range e.Address().Entity {
		parts = append(parts, fmt.Sprintf("%d", a))
	}
	return joinDots(parts)
}

func joinDots(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "."
		}
		out += p
	}
	return out
}

// Compile-time assertion that App satisfies api.ServiceReaderInterface.
// If a method is missing or has the wrong signature, this fails at build time
// rather than at runtime.
var _ api.ServiceReaderInterface = (*App)(nil)

// Compile-time assertion that App satisfies spineapi.EventHandlerInterface
// (requires only HandleEvent(EventPayload)).
var _ spineapi.EventHandlerInterface = (*App)(nil)
