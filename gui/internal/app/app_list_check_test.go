package app

import (
	"path/filepath"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

func TestAppListCheckDeduplicatesAndKeepsInitialBadge(t *testing.T) {
	a := New(Config{ProfilesPath: filepath.Join(t.TempDir(), "profiles.json")})
	a.prepareInitialAppIconGate("K80")
	a.mu.Lock()
	a.devices = []adb.Device{{Serial: "K80", Identity: "K80"}}
	a.appListBusy["K80"] = time.Now()
	a.mu.Unlock()

	// ordinary readiness enumeration retains its existing visible busy marker.
	a.mu.Lock()
	devs := a.devicesWithAppBusyLocked()
	a.mu.Unlock()
	if !devs[0].AppBusy {
		t.Fatal("ready-edge enumeration should keep its existing busy badge")
	}

	// A click joins it without bypassing the one-time empty-cache badge or starting a second run.
	watching, err := a.CheckAppList("K80")
	if err != nil || !watching {
		t.Fatalf("click should join the active enumeration: watching=%v err=%v", watching, err)
	}
	status, err := a.IsAppListCheckBusy("K80")
	if err != nil || !status.Busy || status.Changed {
		t.Fatalf("unexpected silent status while enumeration runs: %+v err=%v", status, err)
	}
	a.mu.Lock()
	if started := a.beginAppListCheckLocked("K80"); started {
		t.Fatal("repeated click must join an existing device enumeration")
	}
	devs = a.devicesWithAppBusyLocked()
	if !devs[0].AppBusy {
		t.Fatal("a silent check must not bypass the initial empty-cache badge")
	}
	firstBusy := a.appListBusy["K80"]
	if started := a.beginAppListCheckLocked("K80"); started {
		t.Fatal("repeated click must not start a duplicate enumeration")
	}
	if !a.appListBusy["K80"].Equal(firstBusy) {
		t.Fatal("duplicate click must not replace or refresh the active enumeration")
	}
	a.mu.Unlock()
}

func TestBeginAppListCheckStartsOnlyOnce(t *testing.T) {
	a := New(Config{ProfilesPath: filepath.Join(t.TempDir(), "profiles.json")})
	a.mu.Lock()
	if !a.beginAppListCheckLocked("K80") {
		t.Fatal("first click should start a check")
	}
	if a.beginAppListCheckLocked("K80") {
		t.Fatal("second click should join the active check")
	}
	if !a.appListSilent["K80"] {
		t.Fatal("click check should be marked silent")
	}
	a.mu.Unlock()
}
