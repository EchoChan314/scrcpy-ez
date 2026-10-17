package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
)

// NativeDisplay is independent of user profiles and the currently active screen mode.
// FPS is a capture ceiling: it may come from any supported physical-screen mode.
type NativeDisplay struct {
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	LongEdge        int    `json:"longEdge"`
	FPS             int    `json:"fps"`
	Source          string `json:"source,omitempty"`
	DPI             int    `json:"dpi,omitempty"`
	DensityLongEdge int    `json:"densityLongEdge,omitempty"`
}

func parseDisplayCapabilities(out string) (string, NativeDisplay, error) {
	const marker = "SCEZ_DISPLAY_CAPS:"
	pos := strings.Index(out, marker)
	if pos < 0 {
		return "", NativeDisplay{}, errors.New("display capabilities missing")
	}
	line := strings.SplitN(out[pos+len(marker):], "\n", 2)[0]
	var wire struct {
		Version   int    `json:"version"`
		Serial    string `json:"serial"`
		DisplayID int    `json:"displayId"`
		Modes     []struct {
			Width  int     `json:"width"`
			Height int     `json:"height"`
			Hz     float64 `json:"refreshHz"`
		} `json:"modes"`
	}
	if len(line) > 65536 || json.Unmarshal([]byte(line), &wire) != nil || wire.Version != 1 || wire.DisplayID != 0 || len(wire.Modes) == 0 || len(wire.Modes) > 256 {
		return "", NativeDisplay{}, errors.New("invalid display capabilities")
	}
	var native NativeDisplay
	var maxHz float64
	for _, mode := range wire.Modes {
		if mode.Width < 16 || mode.Height < 16 || mode.Width > 32768 || mode.Height > 32768 || math.IsNaN(mode.Hz) || math.IsInf(mode.Hz, 0) || mode.Hz < 1 || mode.Hz > 1000 {
			return "", NativeDisplay{}, errors.New("invalid display mode")
		}
		if int64(mode.Width)*int64(mode.Height) > int64(native.Width)*int64(native.Height) {
			native.Width, native.Height = mode.Width, mode.Height
		}
		maxHz = math.Max(maxHz, mode.Hz)
	}
	native.LongEdge = max(native.Width, native.Height)
	native.FPS = int(math.Round(maxHz))
	native.Source = "display-modes"
	return adb.StableSerial(wire.Serial), native, nil
}

// This is called only by the existing background physical-spec warmup. It uses
// the content-addressed helper, so a live cast's server is never overwritten.
func (a *App) readNativeDisplay(identity, serial string) (NativeDisplay, error) {
	helper, err := a.appCatalogServer()
	if err != nil {
		return NativeDisplay{}, err
	}
	// Keep metadata deployment independent of an app-catalog upload in flight.
	helper.remote += "-display"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := helper.command("")
	command = strings.Replace(command, "com.genymobile.scrcpy.Server "+helper.version+" cleanup=false ", "com.genymobile.scrcpy.device.DisplayCapabilities", 1)
	query := func() (string, error) { return a.adb.ShellOutStream(ctx, serial, command, 96<<10, nil) }
	out, err := query()
	if err != nil && (strings.Contains(out, appHelperMissingMarker) || strings.Contains(out, "ClassNotFoundException")) {
		if err = a.adb.PushFile(ctx, serial, helper.local, helper.remote); err == nil {
			out, err = query()
		}
	}
	if err != nil && ctx.Err() == nil {
		// ADB/Android may briefly reject app_process during a busy reconnect.
		// Retry the read once, within the same hard deadline; no server restart.
		select {
		case <-ctx.Done():
		case <-time.After(250 * time.Millisecond):
			out, err = query()
		}
	}
	if err != nil {
		return NativeDisplay{}, fmt.Errorf("display query: %w (%s)", err, strings.TrimSpace(out))
	}
	owner, native, err := parseDisplayCapabilities(out)
	if err != nil {
		return NativeDisplay{}, err
	}
	if a.appListKeyFor(serial) != identity {
		return NativeDisplay{}, errors.New("display owner changed")
	}
	if entry, ok := a.profiles.Entry(identity); owner != "" && (!ok || !contains(entry.Serials, owner)) {
		return NativeDisplay{}, fmt.Errorf("display physical identity mismatch")
	}
	return native, nil
}

// GetNativeDisplay is a nonblocking bridge endpoint. Polling it never launches
// duplicate ADB work, and failures retain the last successful physical snapshot.
func (a *App) GetNativeDisplay(serial string) NativeDisplay {
	identity := a.appListKeyFor(a.profileKeyForSession(serial))
	p := a.devicePhysCached(identity)
	transport := a.bestEnumSerial(identity, "")
	if a.cfg.AdbPath != "" && transport != "" {
		go a.guard("display-capabilities", func() { a.devicePhys(transport) })
	}
	native := p.native
	native.DPI, native.DensityLongEdge = p.dpi, p.longSide
	return native
}

func applyNativeBaseline(p *DeviceProfile, native NativeDisplay) {
	// WiFi uses a conservative default independently of the physical ceiling.
	p.Wifi.Baseline.FPS = 60
	if !p.Wifi.Custom {
		p.Wifi.FPS = 60
	}
	if native.LongEdge <= 0 && native.FPS <= 0 {
		return
	}
	for i, mode := range []*ModeProfile{&p.Usb, &p.Wifi} {
		if mode.Baseline.Res <= 0 {
			mode.Baseline.Res = mode.Res
		}
		if mode.Baseline.FPS <= 0 {
			mode.Baseline.FPS = mode.FPS
		}
		if native.LongEdge > 0 {
			mode.Baseline.Res = native.LongEdge
		}
		if i == 0 && native.FPS > 0 {
			mode.Baseline.FPS = native.FPS
		}
		if !mode.Custom {
			mode.Res, mode.FPS = mode.Baseline.Res, mode.Baseline.FPS
		}
	}
}

func applyNativeCastParams(params *bridge.CastParams, p DeviceProfile, native NativeDisplay) {
	if native.LongEdge == 0 && native.FPS == 0 {
		return
	}
	for i, mode := range []ModeProfile{p.Usb, p.Wifi} {
		target := &params.Usb
		if i == 1 {
			target = &params.Wifi
		}
		if mode.Custom || target.Set {
			continue
		}
		bitrate := mode.Baseline.Bitrate
		if bitrate <= 0 {
			if i == 0 {
				bitrate = 60
			} else {
				bitrate = 15
			}
		}
		res, fps := mode.Res, mode.FPS
		if res <= 0 {
			if i == 0 {
				res = 2560
			} else {
				res = 1920
			}
		}
		if fps <= 0 {
			fps = 60
		}
		*target = bridge.ModeParams{Res: res, FPS: fps, Bitrate: bitrate, Set: true, LockFps: mode.LockFps, LockBitrate: mode.LockBitrate}
	}
}
