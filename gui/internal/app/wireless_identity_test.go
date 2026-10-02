package app

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
)

func wirelessTestDevice(addr string) adb.Device {
	return adb.Device{Serial: addr, State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro", Marketname: "Xiaomi Pad 8 Pro", Res: "3200x2136"}
}

func quietWirelessApp(t *testing.T) *App {
	t.Helper()
	a := New(Config{ProfilesPath: filepath.Join(t.TempDir(), "profiles.json")})
	// This test concerns identity only; suppress unrelated external app enumeration.
	a.appListLastReady["device:PAD_A"] = true
	a.appListLastReady["device:PAD_B"] = true
	a.appListBusy["device:PAD_A"] = time.Now()
	a.appListBusy["device:PAD_B"] = time.Now()
	return a
}

func waitWirelessLearn(t *testing.T, a *App, check *wirelessIdentityCheck) {
	t.Helper()
	until := time.Now().Add(4 * time.Second)
	for time.Now().Before(until) {
		a.mu.RLock()
		busy := check.busy
		a.mu.RUnlock()
		if !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("wireless learning did not finish")
}

func TestWirelessPendingLearnsArchiveRenameAndSession(t *testing.T) {
	a := quietWirelessApp(t)
	addr := "192.0.2.11:5555"
	devs := []adb.Device{wirelessTestDevice(addr)}
	jobs := a.prepareWirelessIdentityChecks(devs)
	a.annotateDeviceIdentities(devs)
	check := jobs[0]
	a.lastTrack = devs
	a.devices = devs
	st := &sessionState{identity: "pending:" + addr, identityCheck: check, cast: CastState{Mode: "wifi", Spec: &bridge.Spec{MaxSize: 1920, FPS: 120, Mbps: 15}}}
	a.sessions[addr] = st
	a.appWins[appWinKey(addr, "test.app")] = &appWinState{identity: st.identity, identityCheck: check}
	pendingKey := "pending:" + addr + "|" + strconv.FormatUint(check.epoch, 10)
	if err := a.RenameDevices(map[string]string{pendingKey: "我的平板"}); err != nil {
		t.Fatal(err)
	}
	if len(a.profiles.Entries()) != 0 {
		t.Fatal("pending rename created an address archive")
	}
	a.pairOps.getpropFn = func(_ context.Context, target, prop string) (string, error) {
		if target != addr {
			t.Errorf("wrong query target %s", target)
		}
		if prop == "ro.serialno" {
			return "PAD_A", nil
		}
		return "", nil
	}
	a.learnWirelessIdentity(check)
	reloaded := NewProfileStore(a.profiles.Path())
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	e, ok := reloaded.Entry("device:PAD_A")
	if !ok || e.DisplayName != "我的平板" || e.Profiles.Wifi.Baseline.FPS != 120 {
		t.Fatalf("archive not promoted/persisted: %+v", e)
	}
	if st.identity != "device:PAD_A" || a.appWins[appWinKey(addr, "test.app")].identity != st.identity {
		t.Fatal("sessions did not follow confirmed archive")
	}
	if d := a.Snapshot().Devices[0]; d.Identity != st.identity || d.Name != "我的平板" {
		t.Fatalf("card differs from archive: %+v", d)
	}
	usb := []adb.Device{{Serial: "PAD_A", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro"}}
	a.profiles.SyncDevices(usb)
	applyProfileNames(usb, a.profiles)
	if usb[0].Identity != st.identity || usb[0].Name != "我的平板" {
		t.Fatal("rename lost on USB switch")
	}
}

func TestWirelessIdentityDiscardLateResultAndRenameOnAddressReuse(t *testing.T) {
	a := quietWirelessApp(t)
	addr := "192.0.2.11:5555"
	devs := []adb.Device{wirelessTestDevice(addr)}
	old := a.prepareWirelessIdentityChecks(devs)[0]
	a.lastTrack = devs
	if _, err := a.renamePendingDevice("pending:"+addr+"|"+strconv.FormatUint(old.epoch, 10), "旧平板"); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	a.pairOps.getpropFn = func(context.Context, string, string) (string, error) { close(entered); <-release; return "PAD_A", nil }
	go a.learnWirelessIdentity(old)
	<-entered
	a.prepareWirelessIdentityChecks(nil)
	current := a.prepareWirelessIdentityChecks(devs)[0]
	a.lastTrack = devs
	if _, err := a.renamePendingDevice("pending:"+addr+"|"+strconv.FormatUint(old.epoch, 10), "不能落到新主人"); err == nil {
		t.Fatal("accepted stale rename token")
	}
	close(release)
	waitWirelessLearn(t, a, old)
	if len(a.profiles.Entries()) != 0 || current.serial != "" || current.nameSet {
		t.Fatal("late result or name leaked into new transport epoch")
	}
	a.commitWirelessIdentity(current, "PAD_B")
	if e, _ := a.profiles.Entry("device:PAD_B"); e.DisplayNameSet {
		t.Fatal("old name followed reused IP")
	}
}

func TestWirelessLearningBoundedAndBootFallback(t *testing.T) {
	a := quietWirelessApp(t)
	addr := "192.0.2.12:5555"
	devs := []adb.Device{wirelessTestDevice(addr)}
	check := a.prepareWirelessIdentityChecks(devs)[0]
	a.lastTrack = devs
	var calls atomic.Int32
	a.pairOps.getpropFn = func(context.Context, string, string) (string, error) { calls.Add(1); return "unknown", nil }
	a.learnWirelessIdentity(check)
	if calls.Load() != 6 || len(a.profiles.Entries()) != 0 {
		t.Fatalf("unbounded/invalid learning: calls=%d", calls.Load())
	}
	for i := 0; i < 20; i++ {
		if len(a.prepareWirelessIdentityChecks(devs)) != 0 {
			t.Fatal("unchanged snapshots retriggered identity query")
		}
	}
	a.pairOps.getpropFn = func(_ context.Context, _ string, prop string) (string, error) {
		if prop == "ro.boot.serialno" {
			return "PAD_A", nil
		}
		return addr, nil
	}
	a.mu.Lock()
	a.retryWirelessIdentityLocked(check)
	a.mu.Unlock()
	waitWirelessLearn(t, a, check)
	if check.serial != "PAD_A" {
		t.Fatal("casting retry did not recover through boot serial")
	}
}

func TestWirelessAddressNewOwnerKeepsOriginalCastArchive(t *testing.T) {
	a := quietWirelessApp(t)
	addr := "192.0.2.11:5555"
	a.profiles.SyncDevices([]adb.Device{{Serial: "PAD_A", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro"}})
	a.profiles.AddrSuccessWithMode("device:PAD_A", addr, ModeTcpip)
	a.profiles.SetDisplayName("device:PAD_A", "原平板")
	devs := []adb.Device{wirelessTestDevice(addr)}
	check := a.prepareWirelessIdentityChecks(devs)[0]
	a.lastTrack = devs
	st := &sessionState{identity: "device:PAD_A", identityCheck: check}
	a.sessions[addr] = st
	a.commitWirelessIdentity(check, "PAD_B")
	if st.identity != "device:PAD_A" {
		t.Fatal("original cast switched archive after IP reuse")
	}
	if e, _ := a.profiles.Entry("device:PAD_A"); e.DisplayName != "原平板" || addrInList(e.Addrs, addr) {
		t.Fatalf("old archive changed incorrectly: %+v", e)
	}
	if d := a.Snapshot().Devices[0]; d.Identity != "device:PAD_B" || d.Name == "原平板" {
		t.Fatalf("new device inherited old name: %+v", d)
	}
}

func TestSetAppsUnchangedDoesNotPersistAndMetadataChangesDo(t *testing.T) {
	s := NewProfileStore(filepath.Join(t.TempDir(), "profiles.json"))
	s.SyncDevices([]adb.Device{{Serial: "PAD_A", State: "device", ConnType: "usb"}})
	apps := []AppListItem{{Pkg: "a", Name: "A"}, {Pkg: "b", Name: "B", Sys: true}}
	if err := s.SetApps("device:PAD_A", apps); err != nil {
		t.Fatal(err)
	}
	// Preserve a deliberately old timestamp. A redundant write would replace it.
	stamp := time.Unix(1_500_000_000, 0)
	if err := os.Chtimes(s.Path(), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := s.SetApps("device:PAD_A", []AppListItem{apps[1], apps[0]}); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(s.Path())
	if !info.ModTime().Equal(stamp) {
		t.Fatal("unchanged/reordered app list wrote archive")
	}
	apps[0].Sys = true
	if err := s.SetApps("device:PAD_A", apps); err != nil {
		t.Fatal(err)
	}
	reloaded := NewProfileStore(s.Path())
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	e, ok := reloaded.Entry("device:PAD_A")
	if !ok || len(e.Apps) != 2 || !e.Apps[0].Sys {
		t.Fatal("system-app metadata change was lost")
	}
}

func TestWirelessPendingCastFollowsFreshSuccessAfterADBReset(t *testing.T) {
	a := quietWirelessApp(t)
	addr := "192.0.2.11:5555"
	devs := []adb.Device{wirelessTestDevice(addr)}
	old := a.prepareWirelessIdentityChecks(devs)[0]
	st := &sessionState{identity: "pending:" + addr, identityCheck: old, castAddr: addr, cast: CastState{Active: true, Mode: "wifi", Spec: &bridge.Spec{MaxSize: 1920, FPS: 120, Mbps: 15}}}
	a.sessions[addr] = st
	a.prepareWirelessIdentityChecks(nil)
	current := a.prepareWirelessIdentityChecks(devs)[0]
	a.lastTrack = devs
	a.commitWirelessIdentity(current, "PAD_A")
	if st.identity != "pending:"+addr {
		t.Fatal("discovery attached an old pending session without a fresh success signal")
	}
	a.NotifyLine(addr, "INFO: Texture: 1920x1280")
	if st.identity != "device:PAD_A" || st.identityCheck != current {
		t.Fatal("successful cast did not attach after ADB reset")
	}
	if e, _ := a.profiles.Entry("device:PAD_A"); e.Profiles.Wifi.Baseline.FPS != 120 {
		t.Fatal("cast baseline was lost during identity confirmation")
	}
}
