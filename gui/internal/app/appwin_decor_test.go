package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

func TestAppWinDecorCompatibilityAtLaunch(t *testing.T) {
	cases := []struct {
		name, manufacturer, market string
		otherDecor, noDecor        bool
	}{
		{"xiaomi", "Xiaomi", "REDMI K80", true, true},
		{"xiaomi-other-switch-off", " Xiaomi ", "", false, true},
		{"redmi", "REDMI", "", true, true},
		{"poco", "POCO", "", true, true},
		{"old-xiaomi-metadata", "", "Xiaomi Pad 8 Pro", true, true},
		{"old-redmi-metadata", "", "REDMI K80", true, true},
		{"old-chinese-metadata", "", "小米平板8 Pro", true, true},
		{"samsung-default", "samsung", "Galaxy S23", true, false},
		{"motorola-default", "motorola", "edge 50", true, false},
		{"oneplus-default", "OnePlus", "OnePlus 11", true, false},
		{"oppo-default", "OPPO", "", true, false},
		{"vivo-default", "vivo", "", true, false},
		{"honor-default", "HONOR", "", true, false},
		{"huawei-default", "HUAWEI", "", true, false},
		{"google-default", "Google", "Pixel 8", true, false},
		{"other-manufacturer-authoritative", "samsung", "Xiaomi example", true, false},
		{"unknown-default", "", "Phone", true, false},
		{"brand-substring-is-not-identity", "", "My Xiaomi phone", true, false},
		{"other-brand-opt-in", "samsung", "Galaxy S23", false, true},
		{"unknown-opt-in", "", "", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newAppWinEnv(t)
			setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb",
				Manufacturer: tc.manufacturer, Marketname: tc.market, Name: "POCO renamed by user", Res: "3200x1440", FPS: 120}})
			if err := e.a.SetOtherAppWinSystemDecorations(tc.otherDecor); err != nil {
				t.Fatal(err)
			}
			if err := e.a.StartAppWin("12345TESTA", "com.android.settings", "设置"); err != nil {
				t.Fatal(err)
			}
			p := e.f.waitParams(t, 1)
			if p.VdNoDecor != tc.noDecor {
				t.Fatalf("VdNoDecor=%v, want %v", p.VdNoDecor, tc.noDecor)
			}
			if p.VdDpi != 480 || p.VdSize != "2560x1152" || !p.VdFlex || p.VdIme != "local" {
				t.Fatalf("decoration policy changed display parameters: %+v", p)
			}
		})
	}
}

func TestAppWinDecorCachedMetadataAcrossTransportChange(t *testing.T) {
	e := newAppWinEnv(t)
	usb := adb.Device{Serial: "12345TESTA", State: "device", ConnType: "usb", Manufacturer: "Xiaomi", Marketname: "REDMI K80"}
	e.a.profiles.SyncDevices([]adb.Device{usb})
	setDevices(e.a, []adb.Device{usb})
	if err := e.a.StartAppWin(usb.Serial, "pkg.one", "One"); err != nil {
		t.Fatal(err)
	}
	if !e.f.waitParams(t, 1).VdNoDecor {
		t.Fatal("USB app did not disable decorations")
	}
	e.fireExit(0)
	// A fresh wireless event has identity but has not been enriched yet.
	wifi := adb.Device{Serial: "192.0.2.50:5555", StableSerial: usb.Serial, State: "device", ConnType: "wifi"}
	setDevices(e.a, []adb.Device{wifi})
	if err := e.a.StartAppWin(wifi.Serial, "pkg.one", "One"); err != nil {
		t.Fatal(err)
	}
	if !e.f.waitParams(t, 2).VdNoDecor {
		t.Fatal("wireless app lost Xiaomi policy before enrichment")
	}
	// Offline restart can still recover metadata from the same archive identity.
	setDevices(e.a, nil)
	if !e.a.appWinNoSystemDecorations("device:" + usb.Serial) {
		t.Fatal("offline metadata fallback lost Xiaomi protection")
	}
}

func TestAppWinDecorSurvivesParameterRestart(t *testing.T) {
	e := newAppWinParamsEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "K80", State: "device", ConnType: "usb", Manufacturer: "Xiaomi"}})
	if err := e.a.StartAppWin("K80", "pkg.r", "R"); err != nil {
		t.Fatal(err)
	}
	if !e.f.waitParams(t, 1).VdNoDecor {
		t.Fatal("initial app did not disable decorations")
	}
	if err := e.a.SaveAppWinParams("K80", "pkg.r", `{"mode":"usb","size":"1920x1080","fps":90,"bitrate":16,"flex":true,"audio":"phone"}`); err != nil {
		t.Fatal(err)
	}
	e.f.waitStops(t, 1)
	e.fireExit(1)
	p := e.f.waitParams(t, 2)
	if !p.VdNoDecor || p.VdUsb.Size != "1920x1080" || p.VdUsb.Dpi != 360 {
		t.Fatalf("parameter restart lost protection or display parameters: %+v", p)
	}
}

func TestAppWinDecorDoesNotAffectMainCast(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb", Manufacturer: "Xiaomi"}})
	if err := e.a.SetOtherAppWinSystemDecorations(false); err != nil {
		t.Fatal(err)
	}
	if err := e.a.StartCast("12345TESTA"); err != nil {
		t.Fatal(err)
	}
	p := e.f.waitParams(t, 1)
	if p.VdNoDecor || p.VdSize != "" || p.StartApp != "" {
		t.Fatalf("main cast received virtual-display policy: %+v", p)
	}
}

func TestOtherAppWinDecorLegacySettingsAndIndependentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"showParamOverlay":false,"closeToTray":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewSettingsStore(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if !s.Get().OtherAppWinSystemDecorations {
		t.Fatal("legacy settings changed the untested-brand default")
	}
	if err := s.SetOtherAppWinSystemDecorations(false); err != nil {
		t.Fatal(err)
	}
	if got := s.Get(); got.ShowParamOverlay || !got.CloseToTray {
		t.Fatalf("compatibility setting overwrote existing settings: %+v", got)
	}
	if err := s.Set(true, false); err != nil {
		t.Fatal(err)
	}
	s2 := NewSettingsStore(path)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	if got := s2.Get(); !got.ShowParamOverlay || got.CloseToTray || got.OtherAppWinSystemDecorations {
		t.Fatalf("independent settings writes did not survive reload: %+v", got)
	}
}

func TestAppWinDecorMixedDevicesDoNotSharePolicy(t *testing.T) {
	a, rec := multiTestApp()
	setDevices(a, []adb.Device{
		{Serial: "XIAOMI_A", State: "device", ConnType: "usb", Manufacturer: "Xiaomi"},
		{Serial: "SAMSUNG_B", State: "device", ConnType: "usb", Manufacturer: "samsung"},
	})
	for _, serial := range []string{"XIAOMI_A", "SAMSUNG_B"} {
		a.physMu.Lock()
		a.physCache["device:"+serial] = devPhys{longSide: 3200, dpi: 600, at: time.Now()}
		a.physMu.Unlock()
		if err := a.StartAppWin(serial, "pkg.one", "One"); err != nil {
			t.Fatal(err)
		}
	}
	xiaomi := rec.serial("XIAOMI_A")[0]
	samsung := rec.serial("SAMSUNG_B")[0]
	if !xiaomi.waitParams(t, 1).VdNoDecor || samsung.waitParams(t, 1).VdNoDecor {
		t.Fatal("simultaneous devices shared decoration policy")
	}
}
