package app

import (
	"fmt"
	"path/filepath"
	"scrcpy-ez/gui/internal/adb"
	"testing"
)

// Identical fixture for comparing the old and new stable refresh paths. The
// archive is on disk, so a regression that repeatedly persists is also measured.
func BenchmarkArchiveRefresh(b *testing.B) {
	for _, count := range []int{1, 10, 100} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			s := NewProfileStore(filepath.Join(b.TempDir(), "profiles.json"))
			devs := make([]adb.Device, count)
			for i := range devs {
				devs[i] = adb.Device{Serial: fmt.Sprintf("BENCH_%03d", i), State: "device", ConnType: "usb", Marketname: fmt.Sprintf("Phone %03d", i), Wireless: fmt.Sprintf("192.0.2.%d:5555", i+1)}
			}
			s.SyncDevices(devs)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.SyncDevices(devs)
			}
		})
	}
}
