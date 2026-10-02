package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

func TestInitialIconGateOnlyCompletelyEmptyCache(t *testing.T) {
	a := New(Config{ProfilesPath: filepath.Join(t.TempDir(), "profiles.json")})
	for _, id := range []string{"device:COLD", "device:PARTIAL", "device:WARM"} {
		dir := a.iconsDirFor(id)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
		if id != "device:COLD" {
			if err := os.WriteFile(filepath.Join(dir, "com.example.a.png"), []byte("saved icon"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		a.prepareInitialAppIconGate(id)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for _, id := range []string{"device:COLD", "device:PARTIAL", "device:WARM"} {
		a.appListBusy[id] = now
	}
	if !a.initialAppIconBusyLocked("device:COLD", now) {
		t.Fatal("an index without PNGs must enter the initial mask")
	}
	if a.initialAppIconBusyLocked("device:PARTIAL", now) || a.initialAppIconBusyLocked("device:WARM", now) {
		t.Fatal("any existing device PNG must keep background checks accessible")
	}
}

func TestInitialIconGateFixedDeadlineDoesNotRevive(t *testing.T) {
	a := New(Config{ProfilesPath: filepath.Join(t.TempDir(), "profiles.json")})
	id := "device:COLD"
	a.prepareInitialAppIconGate(id)
	a.mu.Lock()
	a.appListBusy[id] = time.Now()
	deadline := a.appIconInitial[id].deadline
	a.mu.Unlock()
	a.touchAppBusy(id) // progress may renew the worker marker, but never the UI deadline
	a.mu.Lock()
	if a.appIconInitial[id].deadline != deadline {
		t.Fatal("progress extended the initial deadline")
	}
	if a.initialAppIconBusyLocked(id, deadline) || a.initialAppIconBusyLocked(id, deadline.Add(time.Second)) {
		t.Fatal("deadline did not release the button")
	}
	gate := a.appIconInitial[id]
	gate.deadline = time.Now().Add(-time.Second)
	a.appIconInitial[id] = gate
	a.mu.Unlock()
	a.prepareInitialAppIconGate(id)
	a.touchAppBusy(id)
	a.mu.Lock()
	if a.initialAppIconBusyLocked(id, time.Now()) {
		t.Fatal("repeated checks revived an expired first-time mask")
	}
	a.mu.Unlock()
}

func TestInitialIconGateCompletesAndFollowsImmutableIdentity(t *testing.T) {
	a, _ := multiTestApp()
	a.profiles.path = filepath.Join(t.TempDir(), "profiles.json")
	a.profiles.SyncDevices([]adb.Device{identityPhone("PHONE_A"), identityPhone("PHONE_B")})
	a.prepareInitialAppIconGate("device:PHONE_A")
	a.mu.Lock()
	a.devices = []adb.Device{{Serial: "USB_A", Identity: "device:PHONE_A"}, {Serial: "192.0.2.11:5555", Identity: "device:PHONE_A"}, {Serial: "192.0.2.12:5555", Identity: "device:PHONE_B"}}
	a.appListBusy["device:PHONE_A"] = time.Now()
	a.appListSilent["device:PHONE_A"] = true
	devs := a.devicesWithAppBusyLocked()
	if !devs[0].AppBusy || !devs[1].AppBusy || devs[2].AppBusy {
		t.Fatal("USB/WiFi gating mixed device identities", devs)
	}
	a.mu.Unlock()
	a.clearAppBusy("device:PHONE_A")
	a.mu.Lock()
	a.appListBusy["device:PHONE_A"] = time.Now() // a failed/finished first run must not remask retries
	if a.initialAppIconBusyLocked("device:PHONE_A", time.Now()) {
		t.Fatal("completion did not permanently release the initial gate")
	}
	a.mu.Unlock()
}
