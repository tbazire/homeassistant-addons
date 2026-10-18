// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Tests for the tracker rebinding contract (scanner.go).
//
// spine-go builds a fresh remote device object on every SHIP (re)connection
// and re-fires EntityChange+Add for every entity, while the scanner keeps one
// tracker per SKI. These tests pin the two rules that keep that pair correct:
//   - the tracker must follow the new generation (dropped on disconnect,
//     rebuilt on the next scan) — otherwise every read targets the closed
//     connection and every render replays the stale cache (frozen HA entities);
//   - a late disconnect event of an older generation must never wipe scan
//     state a newer connection already rebuilt (application events are
//     dispatched asynchronously by spine-go).
//
// The fakes are used as pointers on purpose: two equal-looking values would
// compare equal as interfaces and defeat the generation-identity checks. They
// implement only what the tracker paths call; the embedded interfaces panic on
// anything else (and are never called: NewFeature rejects a nil local entity
// before touching the remote entity).

package scanner

import (
	"testing"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

type fakeRemoteDevice struct {
	spineapi.DeviceRemoteInterface
	ski string
}

func (f *fakeRemoteDevice) Ski() string { return f.ski }

type fakeRemoteEntity struct {
	spineapi.EntityRemoteInterface
	addr *model.EntityAddressType
}

func (f *fakeRemoteEntity) Address() *model.EntityAddressType { return f.addr }

func (f *fakeRemoteEntity) EntityType() model.EntityTypeType { return model.EntityTypeTypeCEM }

// scanOne runs one ScanEntity against scanner s for device dev on address
// 3.1 and asserts a tracker for dev.Ski() exists afterwards.
func scanOne(t *testing.T, s *Scanner, dev spineapi.DeviceRemoteInterface) {
	t.Helper()
	s.ScanEntity(dev, &fakeRemoteEntity{
		addr: &model.EntityAddressType{Entity: []model.AddressEntityType{3, 1}},
	})
	s.mu.Lock()
	_, ok := s.trackers[dev.Ski()]
	s.mu.Unlock()
	if !ok {
		t.Fatalf("expected a tracker for ski %s after ScanEntity", dev.Ski())
	}
}

func TestDropDeviceForGenerationOnlyDropsMatchingGeneration(t *testing.T) {
	const ski = "aa11"
	s := NewScanner(nil, Options{})
	dev := &fakeRemoteDevice{ski: ski}
	scanOne(t, s, dev)

	other := &fakeRemoteDevice{ski: ski} // same SKI, different generation
	if s.DropDeviceForGeneration(ski, other) {
		t.Error("a different generation must not drop the tracker")
	}
	s.mu.Lock()
	_, ok := s.trackers[ski]
	s.mu.Unlock()
	if !ok {
		t.Fatal("tracker was dropped by a non-matching generation")
	}

	if !s.DropDeviceForGeneration(ski, dev) {
		t.Error("the dead generation must drop the tracker")
	}
	s.mu.Lock()
	_, ok = s.trackers[ski]
	s.mu.Unlock()
	if ok {
		t.Error("tracker survived DropDeviceForGeneration for its own generation")
	}
}

func TestDropDeviceForGenerationWithoutTrackerIsNoop(t *testing.T) {
	s := NewScanner(nil, Options{})
	dev := &fakeRemoteDevice{ski: "bb22"}
	if s.DropDeviceForGeneration("bb22", dev) {
		t.Error("dropping an unknown SKI must report false, not panic")
	}
}

func TestDropDeviceIfStaleKeepsCurrentGeneration(t *testing.T) {
	const ski = "cc33"
	s := NewScanner(nil, Options{})
	dev := &fakeRemoteDevice{ski: ski}
	scanOne(t, s, dev)

	if s.DropDeviceIfStale(ski, dev) {
		t.Error("a tracker bound to the current generation must be kept")
	}
	s.mu.Lock()
	tracker := s.trackers[ski]
	s.mu.Unlock()
	if tracker == nil {
		t.Fatal("current-generation tracker was dropped")
	}

	// A connection became active on a newer generation: the old tracker is
	// obsolete and must go.
	fresh := &fakeRemoteDevice{ski: ski}
	if !s.DropDeviceIfStale(ski, fresh) {
		t.Error("a tracker bound to another generation must be dropped")
	}
	if s.DropDeviceIfStale(ski, fresh) {
		t.Error("dropping an already-absent tracker must report false")
	}
}

// TestScanEntityRebindsAfterDrop is the regression test for the frozen-entities
// bug: after a reconnection the same (ski, entity address) must scan again on
// the new generation — ScanEntity's per-address idempotency guard applies to a
// generation, not forever.
func TestScanEntityRebindsAfterDrop(t *testing.T) {
	const ski = "dd44"
	s := NewScanner(nil, Options{})
	dev1 := &fakeRemoteDevice{ski: ski}
	ent := &fakeRemoteEntity{addr: &model.EntityAddressType{Entity: []model.AddressEntityType{3, 1}}}

	s.ScanEntity(dev1, ent)
	s.DropDeviceForGeneration(ski, dev1)

	dev2 := &fakeRemoteDevice{ski: ski}
	s.ScanEntity(dev2, ent)

	s.mu.Lock()
	tracker, ok := s.trackers[ski]
	if ok {
		if tracker.device != dev2 {
			t.Error("tracker must be bound to the new generation")
		}
		if _, scanned := tracker.scannedEntities["3.1"]; !scanned {
			t.Error("entity 3.1 must be marked scanned on the new tracker")
		}
	}
	s.mu.Unlock()
	if !ok {
		t.Fatal("no tracker rebuilt after re-scan on the new generation")
	}
}
