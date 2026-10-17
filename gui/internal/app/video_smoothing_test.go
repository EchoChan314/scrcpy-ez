package app

import (
	"os"
	"path/filepath"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

func TestVideoSmoothingPersistenceAndFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"keepDeviceAwake":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewSettingsStore(path)
	if err := s.Load(); err != nil || s.Get().VideoSmoothing || s.Get().VideoSmoothingFrames != 2 {
		t.Fatal("old settings must keep smoothing disabled", err)
	}
	for _, enabled := range []bool{true, false} {
		if err := s.SetVideoSmoothing(enabled); err != nil {
			t.Fatal(err)
		}
		reloaded := NewSettingsStore(path)
		if err := reloaded.Load(); err != nil || reloaded.Get().VideoSmoothing != enabled || reloaded.Get().KeepDeviceAwake {
			t.Fatal("smoothing must persist independently", err)
		}
	}
	blocked := NewSettingsStore(filepath.Join(path, "blocked.json"))
	if err := blocked.SetVideoSmoothing(true); err == nil || blocked.Get().VideoSmoothing {
		t.Fatal("failed save must roll back")
	}
}

func TestVideoSmoothingFrameBudgetMigrationPersistenceAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	for _, raw := range []string{`{"videoSmoothing":true}`, `{"videoSmoothing":true,"videoSmoothingFrames":3}`} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		s := NewSettingsStore(path)
		if err := s.Load(); err != nil || !s.Get().VideoSmoothing || s.Get().VideoSmoothingFrames != 2 {
			t.Fatal(s.Get(), err)
		}
	}
	s := NewSettingsStore(path)
	for _, frames := range []int{1, 2} {
		if err := s.SetVideoSmoothingFrames(frames); err != nil {
			t.Fatal(err)
		}
		reloaded := NewSettingsStore(path)
		if err := reloaded.Load(); err != nil || reloaded.Get().VideoSmoothingFrames != frames {
			t.Fatal(reloaded.Get(), err)
		}
	}
	if err := s.SetVideoSmoothingFrames(0); err == nil || s.Get().VideoSmoothingFrames != 2 {
		t.Fatal("invalid budget accepted")
	}
	blocked := NewSettingsStore(filepath.Join(path, "blocked.json"))
	if err := blocked.SetVideoSmoothingFrames(1); err == nil || blocked.Get().VideoSmoothingFrames != 2 {
		t.Fatal("failed budget save did not roll back")
	}
}

func TestVideoSmoothingFutureSessionsAndRestart(t *testing.T) {
	for _, wifi := range []bool{false, true} {
		for _, appWindow := range []bool{false, true} {
			e := newAppWinEnv(t)
			serial := "PHONE_TEST"
			if wifi {
				serial = "192.0.2.10:5555"
			}
			setDevices(e.a, []adb.Device{{Serial: serial, StableSerial: "PHONE_TEST", State: "device"}})
			start := func() error {
				if appWindow {
					return e.a.StartAppWin(serial, "pkg.test", "Test")
				}
				return e.a.StartCast(serial)
			}
			if err := start(); err != nil {
				t.Fatal(err)
			}
			p := e.f.waitParams(t, 1)
			if !p.VideoSmoothingSet || p.VideoSmoothing || p.VideoSmoothingFrames != 2 {
				t.Fatal("default must explicitly disable buffering")
			}
			if err := e.a.SetVideoSmoothingFrames(1); err != nil {
				t.Fatal(err)
			}
			if err := e.a.SetVideoSmoothing(true); err != nil {
				t.Fatal(err)
			}
			if len(e.f.paramsLogCopy()) != 1 {
				t.Fatal("setting must not restart active sessions")
			}
			// Existing restart paths obtain a fresh settings snapshot.
			if appWindow {
				if err := e.a.StopAppWin(serial, "pkg.test"); err != nil {
					t.Fatal(err)
				}
			} else {
				e.a.StopCast(serial)
			}
			e.fireExit(0)
			if err := start(); err != nil {
				t.Fatal(err)
			}
			p = e.f.waitParams(t, 2)
			if !p.VideoSmoothingSet || !p.VideoSmoothing || p.VideoSmoothingFrames != 1 || (appWindow && p.KeepDeviceAwake) {
				t.Fatal("new session lost smoothing or app sleep isolation", p)
			}
		}
	}
}
