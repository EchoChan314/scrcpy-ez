package adb

import (
	"context"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/deviceevents"
)

func TestEnrichmentCannotReplayOldUSBTopology(t *testing.T) {
	m := New("unused-adb", "")
	// Fresh caches keep this test independent of external commands. Block the
	// identity/property stage after the old USB snapshot has been captured.
	now := time.Now()
	for _, serial := range []string{"PHONE", "192.0.2.20:5555"} {
		m.cache[serial] = &cachedSpec{identity: "device:PHONE", name: "Phone", marketname: "Phone", at: now, specAt: now, batAt: now}
	}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	m.identityResolver = func(string) (string, string) {
		once.Do(func() { close(started); <-release })
		return "device:PHONE", "PHONE"
	}
	hub := m.EventHub()
	hub.Publish(deviceevents.Snapshot{Epoch: 1, Available: true, Transports: []deviceevents.Transport{{Serial: "PHONE", State: "device", Kind: "usb", ID: "10"}}})
	old := hub.Current()
	var seen []TrackEvents
	track := m.NewTrack(func(ev TrackEvents) { seen = append(seen, ev) })
	done := make(chan bool, 1)
	go func() { done <- track.consumeSnapshot(context.Background(), old) }()
	<-started
	hub.Publish(deviceevents.Snapshot{Epoch: 1, Available: true, Transports: []deviceevents.Transport{{Serial: "192.0.2.20:5555", State: "device", Kind: "wifi", ID: "11"}}})
	close(release)
	if <-done || len(seen) != 0 {
		t.Fatalf("stale USB was delivered after removal: %+v", seen)
	}
	if !track.consumeSnapshot(context.Background(), hub.Current()) || len(seen) != 1 || seen[0].Devices[0].ConnType != "wifi" || seen[0].Raw == nil {
		t.Fatalf("latest wireless snapshot was not delivered: %+v", seen)
	}
}
