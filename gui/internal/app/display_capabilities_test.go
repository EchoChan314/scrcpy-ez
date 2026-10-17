package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
)

func TestNativeDisplayModesIndependentCeilings(t *testing.T) {
	// The highest Hz may be offered at a smaller physical resolution. Capture
	// ceilings are independent; do not reject that combination in the GUI.
	out := `noise SCEZ_DISPLAY_CAPS:{"version":1,"serial":"PHONE","displayId":0,"modes":[{"width":1440,"height":3200,"refreshHz":60.0000038},{"width":1080,"height":2400,"refreshHz":144.0000153}]}`
	owner, got, err := parseDisplayCapabilities(out)
	if err != nil || owner != "PHONE" || got.LongEdge != 3200 || got.FPS != 144 || got.Width != 1440 {
		t.Fatal(owner, got, err)
	}
	for _, modes := range []string{`[]`, `[{"width":0,"height":3200,"refreshHz":120}]`, `[{"width":1440,"height":3200,"refreshHz":0}]`, `[{"width":1440,"height":3200,"refreshHz":1001}]`} {
		if _, _, err := parseDisplayCapabilities(fmt.Sprintf(`SCEZ_DISPLAY_CAPS:{"version":1,"displayId":0,"modes":%s}`, modes)); err == nil {
			t.Fatal("invalid mode accepted", modes)
		}
	}
	if _, _, err := parseDisplayCapabilities(`SCEZ_DISPLAY_CAPS:{"version":1,"displayId":3,"modes":[{"width":4000,"height":6000,"refreshHz":240}]}`); err == nil {
		t.Fatal("virtual display accepted")
	}
}

func TestNativeDefaultsOverrideHistoricalBaselinesButPreserveCustom(t *testing.T) {
	e := newAppWinParamsEnv(t)
	p := DefaultProfile()
	p.Usb.Baseline = Baseline{Res: 2560, FPS: 120, Bitrate: 60}
	p.Wifi.Baseline = Baseline{Res: 1920, FPS: 60, Bitrate: 15}
	p.Wifi.Custom = true
	p.Wifi.Res, p.Wifi.FPS = 1800, 75
	if err := e.a.profiles.Save("device:K80", p); err != nil {
		t.Fatal(err)
	}
	e.a.physCache["device:K80"] = devPhys{longSide: 3200, dpi: 600, at: time.Now(), native: NativeDisplay{Width: 1440, Height: 3200, LongEdge: 3200, FPS: 144}}
	got := e.a.effectiveProfile("device:K80")
	if got.Usb.Res != 3200 || got.Usb.FPS != 144 || got.Wifi.Res != 1800 || got.Wifi.FPS != 75 || got.Wifi.Baseline.Res != 3200 || got.Wifi.Baseline.FPS != 60 {
		t.Fatal(got)
	}
	defaults := e.a.appWinDefaults("device:K80")
	if defaults.Usb.Size != "3200" || defaults.Wifi.FPS != 60 {
		t.Fatal(defaults)
	}
	var params bridge.CastParams
	applyNativeCastParams(&params, got, e.a.physCache["device:K80"].native)
	if !params.Usb.Set || params.Usb.Res != 3200 || params.Usb.FPS != 144 || params.Wifi.Set {
		t.Fatal(params)
	}
	phys := e.a.physCache["device:K80"]
	for _, pair := range [][2]int{{3200, 600}, {2560, 480}, {1920, 360}, {1080, 203}} {
		if got := calcVdDpi(phys, pair[0]/2, pair[0]); got != pair[1] {
			t.Fatal(pair, got)
		}
	}
}

func TestNativeDefaultInjectionRetainsPendingCustomAndLocks(t *testing.T) {
	p := DefaultProfile()
	p.Usb.Baseline = Baseline{Res: 3200, FPS: 144, Bitrate: 60}
	p.Wifi.Baseline = Baseline{Res: 3200, FPS: 144, Bitrate: 15}
	p.Wifi.LockFps = true
	native := NativeDisplay{LongEdge: 3200, FPS: 144}
	applyNativeBaseline(&p, native)
	params := bridge.CastParams{Usb: bridge.ModeParams{Res: 1600, FPS: 45, Set: true}}
	applyNativeCastParams(&params, p, native)
	if params.Usb.Res != 1600 || params.Wifi.FPS != 60 || !params.Wifi.LockFps {
		t.Fatal(params)
	}
}

func TestWirelessDefaultFPSOverridesStaleBaselineAndUnknownHardware(t *testing.T) {
	for _, ceiling := range []int{0, 30, 60, 120, 144} {
		t.Run(fmt.Sprint(ceiling), func(t *testing.T) {
			e := newAppWinParamsEnv(t)
			p := DefaultProfile()
			p.Wifi.FPS = 144
			p.Wifi.Baseline = Baseline{Res: 3200, FPS: 144, Bitrate: 15}
			if err := e.a.profiles.Save("device:K80", p); err != nil {
				t.Fatal(err)
			}
			native := NativeDisplay{FPS: ceiling}
			if ceiling > 0 {
				native.LongEdge = 3200
			}
			e.a.physCache["device:K80"] = devPhys{native: native, at: time.Now()}
			got := e.a.effectiveProfile("device:K80")
			if got.Wifi.FPS != 60 || got.Wifi.Baseline.FPS != 60 || e.a.appWinDefaults("device:K80").Wifi.FPS != 60 {
				t.Fatal(got)
			}
			var params bridge.CastParams
			applyNativeCastParams(&params, got, native)
			if ceiling > 0 && params.Wifi.FPS != 60 {
				t.Fatal(params)
			}
			if ceiling > 0 && got.Usb.FPS != ceiling {
				t.Fatal("wired default changed", got)
			}
		})
	}
}

func TestConnectedNativeDisplayDefaults(t *testing.T) {
	runtime, serials := os.Getenv("SCEZ_DISPLAY_TEST_RUNTIME"), os.Getenv("SCEZ_DISPLAY_TEST_SERIALS")
	if runtime == "" || serials == "" {
		t.Skip("connected display verification is opt-in")
	}
	for _, serial := range strings.Split(serials, ",") {
		t.Run(serial, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			owner, err := exec.CommandContext(ctx, filepath.Join(runtime, "adb.exe"), "-s", serial, "shell", "getprop", "ro.serialno").Output()
			if err != nil {
				t.Fatal(err)
			}
			device := adb.BuildDevice(adb.ParseDevices(serial + "\tdevice\n"))
			device.StableSerial = strings.TrimSpace(string(owner))
			a := New(Config{BatPath: filepath.Join(runtime, "投屏支持.bat"), AdbPath: filepath.Join(runtime, "adb.exe"), ProfilesPath: filepath.Join(t.TempDir(), "profiles.json"), Version: "test"})
			a.profiles.SyncDevices([]adb.Device{device})
			a.devices = []adb.Device{device}
			phys := a.devicePhys(serial)
			if phys.native.Source != "display-modes" || phys.native.FPS <= 0 || phys.longSide <= 0 || phys.dpi <= 0 {
				_, readErr := a.readNativeDisplay(a.appListKeyFor(serial), serial)
				t.Log("independent retry:", readErr)
				t.Fatal(phys)
			}
			p := a.effectiveProfile(serial)
			d := a.appWinDefaults(serial)
			if p.Usb.Res != phys.native.LongEdge || p.Wifi.FPS != 60 || d.Wifi.FPS != 60 {
				t.Fatal(p, d)
			}
			t.Logf("native=%dx%d ceiling=%d dpi=%d densityBasis=%d usb=%d/%d wifi=%d/%d", phys.native.Width, phys.native.Height, phys.native.FPS, phys.dpi, phys.longSide, p.Usb.Res, p.Usb.FPS, p.Wifi.Res, p.Wifi.FPS)
		})
	}
}
