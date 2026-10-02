package app

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

func learningTestApp(t *testing.T) (*App, adb.Device) {
	t.Helper()
	a := New(Config{Version: "test", ProfilesPath: filepath.Join(t.TempDir(), "profiles.json")})
	t.Cleanup(a.Close)
	d := adb.Device{Serial: "TEST0930", State: "device", ConnType: "usb", Marketname: "DIAG PHONE", Identity: "DIAG PHONE"}
	a.profiles.SyncDevices([]adb.Device{d})
	d.Identity = a.profiles.ResolveKey(d.Serial)
	a.lastTrack = []adb.Device{d}
	a.appListBusy[d.Identity] = time.Now() // 本组只测试学习，不启动应用枚举
	a.teachOps.getpropFn = func(context.Context, string, string) (string, error) { return "0", nil }
	a.teachOps.shellFn = func(context.Context, string, ...string) (string, error) {
		return "2: wlan0: <UP>\n inet 192.0.2.20/24 scope global wlan0\n", nil
	}
	a.teachOps.tcpipFn = func(context.Context, string, string) error { return nil }
	return a, d
}

func TestProductionUSBOperationsInitialized(t *testing.T) {
	a := New(Config{Version: "v-test", ProfilesPath: filepath.Join(t.TempDir(), "profiles.json")})
	t.Cleanup(a.Close)
	if a.teachOps.getpropFn == nil || a.teachOps.tcpipFn == nil || a.teachOps.shellFn == nil || a.teachOps.probeFn == nil {
		t.Fatal("production USB learning operations must all be initialized")
	}
}

func TestUSBLearningRetryAndJointMaskExit(t *testing.T) {
	a, d := learningTestApp(t)
	var probes, restarts atomic.Int32
	a.teachOps.tcpipFn = func(context.Context, string, string) error { restarts.Add(1); return nil }
	a.teachOps.probeFn = func(context.Context, string) bool { return probes.Add(1) >= 3 }
	a.maybeTeachTcpipEvent(context.Background(), []adb.Device{d}, []adb.Device{d}, nil)
	a.plugStabilityUpdate([]adb.Device{d})
	if !a.plugActiveSerial(d.Serial) {
		t.Fatal("first failed probe must keep mask active")
	}
	waitForMdns(t, "wireless retry and stable USB must jointly release mask", func() bool { return !a.plugActiveSerial(d.Serial) })
	if probes.Load() < 3 || restarts.Load() != 1 {
		t.Fatalf("retry must probe again without restarting adbd again: probes=%d restarts=%d", probes.Load(), restarts.Load())
	}
	saved := NewProfileStore(a.profiles.path)
	if err := saved.Load(); err != nil {
		t.Fatal(err)
	}
	if saved.BestAddr(d.Serial) != "192.0.2.20:5555" {
		t.Fatal("mask released before address was persisted")
	}
}

func TestUSBFirstDeviceDeadlineDoesNotReset(t *testing.T) {
	a, d := learningTestApp(t)
	a.teachOps.probeFn = func(context.Context, string) bool { return false }
	beforeDevice := time.Now().Add(-5 * time.Second)
	a.plugStart(d.Identity, beforeDevice, "unauthorized")
	a.maybeTeachTcpipEvent(context.Background(), []adb.Device{d}, []adb.Device{d}, nil)
	a.teachMu.Lock()
	state := a.usbLearning[d.Serial]
	first := a.plugging[d.Identity]
	deadline, _ := state.ctx.Deadline()
	a.plugStableReady[d.Identity] = true
	a.teachMu.Unlock()
	if !first.After(beforeDevice.Add(4 * time.Second)) {
		t.Fatal("first device must receive its own full 10s learning budget")
	}
	a.plugStabilityCheck(d.Identity)
	if !a.plugActiveSerial(d.Serial) {
		t.Fatal("USB stability alone must not clear mask")
	}
	a.maybeTeachTcpipEvent(context.Background(), nil, nil, nil) // adbd restart gap
	a.maybeTeachTcpipEvent(context.Background(), []adb.Device{d}, []adb.Device{d}, nil)
	a.teachMu.Lock()
	unchanged := a.usbLearning[d.Serial] == state && a.plugging[d.Identity].Equal(first)
	nextDeadline, _ := a.usbLearning[d.Serial].ctx.Deadline()
	a.plugging[d.Identity] = time.Now().Add(-plugShieldTimeout - time.Second)
	a.teachMu.Unlock()
	if !unchanged || !nextDeadline.Equal(deadline) {
		t.Fatal("repeated device must not reset 10s deadline")
	}
	a.plugTimeout(d.Identity)
	if a.plugActiveSerial(d.Serial) || state.ctx.Err() == nil {
		t.Fatal("deadline must clear mask and cancel retry")
	}
}

func TestUSBStabilityResetsWithoutExtendingLearning(t *testing.T) {
	a, d := learningTestApp(t)
	a.teachOps.probeFn = func(context.Context, string) bool { return false }
	a.startUsbLearning(context.Background(), d.Serial)
	a.plugStabilityUpdate([]adb.Device{d})
	a.teachMu.Lock()
	first := a.plugging[d.Identity]
	oldTimer := a.plugStableTimers[d.Identity]
	a.plugStableReady[d.Identity] = true
	a.plugLearned[d.Identity] = true
	a.teachMu.Unlock()
	offline := d
	offline.State = "offline"
	a.plugStabilityUpdate([]adb.Device{offline})
	a.plugStabilityUpdate([]adb.Device{d})
	a.teachMu.Lock()
	reset := !a.plugStableReady[d.Identity] && a.plugStableTimers[d.Identity] != oldTimer
	unchanged := a.plugging[d.Identity].Equal(first)
	a.teachMu.Unlock()
	if !reset || !unchanged {
		t.Fatal("device instability must reset only the continuous 2s timer")
	}
	a.plugStabilityCheck(d.Identity)
	if !a.plugActiveSerial(d.Serial) {
		t.Fatal("restored device must not reuse previous continuous stability")
	}
}

func TestUSBLearningRejectsLateProbeAfterTimeout(t *testing.T) {
	a, d := learningTestApp(t)
	release := make(chan struct{})
	defer close(release)
	a.teachOps.probeFn = func(context.Context, string) bool { <-release; return true }
	done := make(chan struct{})
	go func() { a.startUsbLearning(context.Background(), d.Serial); close(done) }()
	waitForMdns(t, "learning started", func() bool { a.teachMu.Lock(); defer a.teachMu.Unlock(); return a.usbLearning[d.Serial] != nil })
	a.teachMu.Lock()
	a.plugging[d.Identity] = time.Now().Add(-plugShieldTimeout - time.Second)
	a.teachMu.Unlock()
	a.plugTimeout(d.Identity)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled learning did not return")
	}
	if a.profiles.BestAddr(d.Serial) != "" {
		t.Fatal("late result must not write an address after timeout")
	}
}

func TestWirelessLearningReportsPersistenceError(t *testing.T) {
	a, d := learningTestApp(t)
	if err := os.Remove(a.profiles.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(a.profiles.path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := a.alignWirelessIP(d.Serial, "192.0.2.20"); err == nil {
		t.Fatal("directory target must report persistence failure")
	}
}

func TestMdnsAddressSurvivesOldSnapshotAndBothLaunches(t *testing.T) {
	a, f := newTestApp()
	t.Cleanup(a.Close)
	const identity, serial = "device:TEST0930", "TEST0930"
	const oldAddr, newAddr = "192.0.2.10:41000", "192.0.2.20:42000"
	a.profiles.SyncDevices([]adb.Device{{Serial: serial, State: "device", ConnType: "usb", Marketname: identity}})
	a.profiles.AddrSuccessMode(identity, oldAddr, ModeTls)
	a.profiles.MatchMdnsModes([]MdnsMatch{{Name: "adb-TEST0930-AbCdEf", Addr: newAddr, Mode: discovery.MdnsModeTls}})
	a.profiles.SyncDevices([]adb.Device{{Serial: oldAddr, State: "device", ConnType: "wifi", Marketname: identity}})
	if a.wirelessStartAddr(identity) != newAddr {
		t.Fatal("old transport snapshot replaced newer mDNS address")
	}
	a.devices = []adb.Device{{Serial: newAddr, State: "device", ConnType: "wifi", Marketname: identity, Identity: identity}}
	if got := a.appWinLockParams(oldAddr).Addr; got != newAddr {
		t.Fatalf("app window reused old address: %s", got)
	}
	if err := a.StartCast(oldAddr); err != nil {
		t.Fatal(err)
	}
	if got := f.waitParams(t, 1).Addr; got != newAddr {
		t.Fatalf("main cast reused old address: %s", got)
	}
	if a.profiles.addrSuccessIfCurrent(identity, oldAddr, ModeTls) {
		t.Fatal("late mDNS probe accepted retired address")
	}
}

func TestOldSessionAliasDoesNotHijackReusedDHCPAddress(t *testing.T) {
	a, _ := learningTestApp(t)
	const oldAddr, newAddr = "192.0.2.10:41000", "192.0.2.20:42000"
	a.profiles.AddrSuccessMode("device:TEST0930", oldAddr, ModeTls)
	a.profiles.MatchMdnsModes([]MdnsMatch{{Name: "adb-TEST0930-AbCdEf", Addr: newAddr, Mode: discovery.MdnsModeTls}})
	a.profiles.SyncDevices([]adb.Device{{Serial: oldAddr, State: "device", ConnType: "wifi", Marketname: "OTHER PHONE", Model: "other", StableSerial: "OTHER_SERIAL"}})
	if got := a.profiles.ResolveKey(oldAddr); got != "device:OTHER_SERIAL" {
		t.Fatalf("current DHCP owner must override session alias: %s", got)
	}
	if a.wirelessStartAddr("device:TEST0930") != newAddr {
		t.Fatal("DHCP reuse corrupted original device")
	}
}
