//go:build windows

package bridge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/deviceevents"
)

func TestRunnerVideoSmoothingAcrossTransportAndApp(t *testing.T) {
	bat, hub := runnerEventFixture(t)
	for _, enabled := range []bool{false, true} {
		for _, frames := range []int{1, 2} {
			for _, wifi := range []bool{false, true} {
				for _, appWindow := range []bool{false, true} {
					name := fmt.Sprintf("smooth=%v/frames=%d/wifi=%v/app=%v", enabled, frames, wifi, appWindow)
					t.Run(name, func(t *testing.T) {
						serial, kind := "USB_A", "usb"
						if wifi {
							serial, kind = "192.0.2.1:5555", "wifi"
						}
						hub.Publish(deviceevents.Snapshot{Epoch: 1, Available: true, Transports: []deviceevents.Transport{{Serial: serial, Kind: kind, State: "device"}}})
						argsFile := filepath.Join(t.TempDir(), "args.txt")
						t.Setenv("SCEZ_TEST_ARGS_LOG", argsFile)
						ready := make(chan struct{}, 1)
						r := NewBatRunner(bat, filepath.Join(filepath.Dir(bat), "adb.exe"), func(line string) {
							if strings.Contains(line, "SCRCPY_EZ_READY") {
								select {
								case ready <- struct{}{}:
								default:
								}
							}
						}, func(int) {})
						t.Cleanup(func() { _ = r.Stop() })
						params := CastParams{ExpectedSerial: "PHONE_A", VideoSmoothingSet: true, VideoSmoothing: enabled, VideoSmoothingFrames: frames}
						if wifi {
							params.Addr = serial
						} else {
							params.Serial = serial
						}
						if appWindow {
							params.VdSize, params.StartApp = "1280x720", "test.app"
						}
						if err := r.Start(serial, params); err != nil {
							t.Fatal(err)
						}
						select {
						case <-ready:
						case <-time.After(15 * time.Second):
							t.Fatal("client not ready")
						}
						data, err := os.ReadFile(argsFile)
						if err != nil {
							t.Fatal(err)
						}
						var buffers []string
						for _, arg := range strings.Fields(string(data)) {
							if strings.HasPrefix(arg, "--video-buffer=") {
								buffers = append(buffers, arg)
							}
						}
						want := "--video-buffer=0"
						if enabled {
							want = fmt.Sprintf("--video-buffer=auto:%d", frames)
						}
						if len(buffers) != 1 || buffers[0] != want {
							t.Fatalf("buffer option must reach actual client exactly once: %v", buffers)
						}
						if err := r.Stop(); err != nil {
							t.Fatal(err)
						}
					})
				}
			}
		}
	}
}
