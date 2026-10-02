package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

func identityPhone(serial string) adb.Device {
	return adb.Device{Serial: serial, State: "device", ConnType: "usb", Marketname: "SAME PHONE", Model: "SAME_MODEL"}
}

func TestIdentitySameModelDistinctArchives(t *testing.T) {
	s := NewProfileStore(filepath.Join(t.TempDir(), "profiles.json"))
	devs := []adb.Device{identityPhone("PHONE_A"), identityPhone("PHONE_B")}
	s.SyncDevices(devs)
	if len(s.Entries()) != 2 || devs[0].Identity != "device:PHONE_A" || devs[1].Identity != "device:PHONE_B" {
		t.Fatalf("same-model phones merged: %+v", devs)
	}
	p := s.Get("PHONE_A")
	p.Usb.FPS = 90
	p.Usb.Custom = true
	if err := s.Save("PHONE_A", p); err != nil {
		t.Fatal(err)
	}
	if s.Get("PHONE_B").Usb.FPS == 90 {
		t.Fatal("device parameters crossed archives")
	}
	reloaded := NewProfileStore(s.Path())
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Entries()) != 2 || reloaded.ResolveKey("PHONE_A") == reloaded.ResolveKey("PHONE_B") {
		t.Fatal("identities lost after restart")
	}
}

func TestIdentityOldKeySurvivesMetadataFluctuation(t *testing.T) {
	s := NewProfileStore("")
	s.data.Devices["SAME PHONE"] = &DeviceEntry{Marketname: "SAME PHONE", Serials: []string{"PHONE_A"}, Profiles: DefaultProfile()}
	for _, market := range []string{"", "SAME PHONE", "RENAMED MODEL", ""} {
		d := identityPhone("PHONE_A")
		d.Marketname = market
		devs := []adb.Device{d}
		s.SyncDevices(devs)
		if devs[0].Identity != "SAME PHONE" || s.ResolveKey("PHONE_A") != "SAME PHONE" {
			t.Fatal("old archive key changed")
		}
	}
	s.SyncDevices([]adb.Device{identityPhone("PHONE_B")})
	if len(s.Entries()) != 2 {
		t.Fatal("second phone reused old name key")
	}
}

func TestIdentityUsbWifiAndSerialLoss(t *testing.T) {
	s := NewProfileStore("")
	s.SyncDevices([]adb.Device{identityPhone("PHONE_A")})
	s.MatchMdnsModes([]MdnsMatch{{Name: "adb-PHONE_A-Ab12Cd", Addr: "192.0.2.10:44444", Mode: ModeTls}})
	wifi := adb.Device{Serial: "192.0.2.10:44444", ConnType: "wifi", State: "device", StableSerial: "PHONE_A"}
	for _, serial := range []string{"PHONE_A", "", "PHONE_A", ""} {
		wifi.StableSerial = serial
		devs := []adb.Device{identityPhone("PHONE_A"), wifi}
		s.SyncDevices(devs)
		if len(s.Entries()) != 1 || devs[0].Identity != devs[1].Identity {
			t.Fatalf("transport/identity split: %+v", devs)
		}
	}
}

func TestIdentityUnknownWifiRemainsUnarchived(t *testing.T) {
	s := NewProfileStore(filepath.Join(t.TempDir(), "profiles.json"))
	s.SyncDevices([]adb.Device{identityPhone("PHONE_A")})
	before, _ := os.ReadFile(s.Path())
	d := adb.Device{Serial: "192.0.2.20:5555", ConnType: "wifi", State: "device", Marketname: "SAME PHONE", Model: "SAME_MODEL"}
	for i := 0; i < 20; i++ {
		s.SyncDevices([]adb.Device{d})
	}
	after, _ := os.ReadFile(s.Path())
	if len(s.Entries()) != 1 || !bytes.Equal(before, after) {
		t.Fatal("missing identity created or changed permanent archives")
	}
	if err := s.Save("pending:"+d.Serial, DefaultProfile()); err == nil {
		t.Fatal("pending profile was reported saved")
	}
	d.StableSerial = "PHONE_B"
	s.SyncDevices([]adb.Device{d})
	if len(s.Entries()) != 2 || s.ResolveKey("PHONE_B") != "device:PHONE_B" {
		t.Fatal("confirmed phone was not promoted")
	}
}

func TestIdentityPairingDoesNotReuseSameMarket(t *testing.T) {
	s := NewProfileStore("")
	s.SyncDevices([]adb.Device{identityPhone("PHONE_A")})
	if err := s.PairArchive("device:PHONE_A", "PHONE_B", "192.0.2.20:44444", "adb-PHONE_B-Ab12Cd", "SAME PHONE", "SAME_MODEL"); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Entry("PHONE_A")
	b, _ := s.Entry("PHONE_B")
	if len(s.Entries()) != 2 || contains(a.Serials, "PHONE_B") || len(a.Addrs) != 0 || len(b.Addrs) != 1 {
		t.Fatal("pairing polluted first phone")
	}
	if s.LearnIdentity("PHONE_A", "PHONE_B", "adb-PHONE_B-Xy12Zz") {
		t.Fatal("conflicting identity was learned")
	}
	a, _ = s.Entry("PHONE_A")
	if a.TlsGuid != "" {
		t.Fatal("rejected learning changed metadata")
	}
}

func TestIdentityDhcpReuseAndAmbiguousIP(t *testing.T) {
	s := NewProfileStore("")
	s.SyncDevices([]adb.Device{identityPhone("PHONE_A"), identityPhone("PHONE_B")})
	s.MatchMdnsModes([]MdnsMatch{{Name: "adb-PHONE_A-Ab12Cd", Addr: "192.0.2.10:44444", Mode: ModeTls}})
	s.MatchMdnsModes([]MdnsMatch{{Name: "adb-PHONE_B-Xy12Zz", Addr: "192.0.2.10:44444", Mode: ModeTls}})
	d := adb.Device{Serial: "192.0.2.10:44444", StableSerial: "PHONE_B", State: "device", ConnType: "wifi"}
	if s.DeviceKey(&d) != "device:PHONE_B" {
		t.Fatal("old address owner beat confirmed serial")
	}
	a, _ := s.Entry("PHONE_A")
	if len(a.Addrs) != 0 {
		t.Fatal("old phone retains another phone's current address")
	}
	s.AddrSuccessWithMode("PHONE_A", "192.0.2.10:5555", ModeTcpip)
	d.Serial = "192.0.2.10:49999"
	d.StableSerial = ""
	if s.DeviceKey(&d) != "" {
		t.Fatal("ambiguous IP picked a random phone")
	}
}

func TestIdentityMergeNeedsPositiveEvidence(t *testing.T) {
	a := &DeviceEntry{Serials: []string{"PHONE_A"}, Profiles: DefaultProfile()}
	b := &DeviceEntry{Model: "SAME_MODEL", Profiles: DefaultProfile()}
	if mergeEntryLocked(a, b) {
		t.Fatal("empty serial was treated as proof")
	}
	b.Serials = []string{"PHONE_B"}
	if mergeEntryLocked(a, b) {
		t.Fatal("different phones merged")
	}
	b.Serials = []string{"PHONE_A"}
	if !mergeEntryLocked(a, b) {
		t.Fatal("same confirmed identity cannot merge")
	}
}

func TestIdentityStableSyncDoesNotWriteAgain(t *testing.T) {
	s := NewProfileStore(filepath.Join(t.TempDir(), "profiles.json"))
	d := identityPhone("PHONE_A")
	d.Wireless = "192.0.2.10:5555"
	s.SyncDevices([]adb.Device{d})
	attempt := s.lastPersistAttempt
	for i := 0; i < 100; i++ {
		if s.SyncDevices([]adb.Device{d}) {
			t.Fatal("stable snapshot reports persistent changes")
		}
	}
	if !s.lastPersistAttempt.Equal(attempt) {
		t.Fatal("stable refresh rewrote archive")
	}
}

func TestIdentityPersistenceFailureRetries(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewProfileStore(filepath.Join(blocked, "profiles.json"))
	s.SyncDevices([]adb.Device{identityPhone("PHONE_A")})
	if s.SaveError() == "" || s.ResolveKey("PHONE_A") == "" {
		t.Fatal("failed write lost memory or error state")
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	s.lastPersistAttempt = time.Now().Add(-6 * time.Second)
	s.SyncDevices([]adb.Device{identityPhone("PHONE_A")})
	if s.SaveError() != "" {
		t.Fatal("failed archive write was not retried")
	}
	if _, err := os.ReadFile(s.Path()); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityTwoPhonesDisplayCastAndDelete(t *testing.T) {
	a, _ := multiTestApp()
	devs := []adb.Device{identityPhone("PHONE_A"), identityPhone("PHONE_B")}
	a.profiles.SyncDevices(devs)
	setDevices(a, devs)
	if len(a.unifyProfileCards(devs)) != 2 {
		t.Fatal("display merged same-model phones")
	}
	if err := a.StartCast("PHONE_A"); err != nil {
		t.Fatal(err)
	}
	if err := a.StartCast("PHONE_B"); err != nil {
		t.Fatal("second phone rejected:", err)
	}
	if sessionBySerial(t, a, "PHONE_A").Identity == sessionBySerial(t, a, "PHONE_B").Identity {
		t.Fatal("cast sessions share identity")
	}
	e, _ := a.profiles.Entry("PHONE_A")
	a.markDeletedAll("device:PHONE_A", e)
	if a.deletedUsbMatch(&devs[1]) {
		t.Fatal("deleting A hides same-model B")
	}
}

func TestIdentityFreshMdnsOverridesStaleAssociation(t *testing.T) {
	a, _ := multiTestApp()
	a.profiles.SyncDevices([]adb.Device{identityPhone("PHONE_A"), identityPhone("PHONE_B")})
	a.profiles.AddrSuccessWithMode("PHONE_A", "192.0.2.10:44444", ModeTls)
	a.mdns = []discovery.MdnsService{{Name: "adb-PHONE_B-Ab12Cd", Addr: "192.0.2.10:44444", Mode: ModeTls}}
	devs := []adb.Device{{Serial: "192.0.2.10:44444", StableSerial: "PHONE_A", State: "device", ConnType: "wifi"}}
	a.annotateDeviceIdentities(devs)
	if devs[0].StableSerial != "PHONE_B" || devs[0].Identity != "device:PHONE_B" {
		t.Fatalf("stale association won: %+v", devs)
	}
	if key, serial := a.confirmedADBIdentity("192.0.2.10:44444"); key != "device:PHONE_B" || serial != "PHONE_B" {
		t.Fatal("raw ADB grouping would lose the new owner")
	}
}

func TestIdentityConflictingLegacyClaimsCannotBeOverwritten(t *testing.T) {
	s := NewProfileStore("")
	s.data.Devices["OLD A"] = &DeviceEntry{Serials: []string{"DUPLICATE"}, Profiles: DefaultProfile()}
	s.data.Devices["device:DUPLICATE"] = &DeviceEntry{Serials: []string{"OTHER"}, Profiles: DefaultProfile()}
	devs := []adb.Device{identityPhone("DUPLICATE")}
	s.SyncDevices(devs)
	if devs[0].Identity != "pending:DUPLICATE" || len(s.Entries()) != 2 {
		t.Fatal("conflict created/overwrote an archive")
	}
	e := s.data.Devices["device:DUPLICATE"]
	if !contains(e.Serials, "OTHER") {
		t.Fatal("existing archive overwritten")
	}
	if err := s.PairArchive("OLD A", "DUPLICATE", "192.0.2.10:44444", "", "", ""); err == nil {
		t.Fatal("ambiguous pairing overwrote an archive")
	}
	a := &DeviceEntry{Serials: []string{"A"}, TlsGuid: "same-guid"}
	b := &DeviceEntry{Serials: []string{"B"}, TlsGuid: "same-guid"}
	if sameArchiveIdentity(a, b) {
		t.Fatal("guid overrode conflicting serials")
	}
}

func TestIdentityLateSpecsStayWithTheirOriginalArchive(t *testing.T) {
	a, _ := multiTestApp()
	t.Cleanup(a.Close)
	devs := []adb.Device{identityPhone("PHONE_A"), identityPhone("PHONE_B")}
	a.profiles.SyncDevices(devs)
	setDevices(a, devs)
	addr := "192.0.2.10:5555"
	a.profiles.AddrSuccessMode("PHONE_A", addr, ModeTcpip)
	if err := a.StartCast(addr); err != nil {
		t.Fatal(err)
	}
	a.profiles.AddrSuccessMode("PHONE_B", addr, ModeTcpip)
	line := "[高清] 有线模式：检测到设备 2136x3200@120Hz，有线规格 h264/80M/2560/120fps（低延迟优化，剪贴板自动同步（电脑复制即达手机））"
	a.NotifyLine(addr, line)
	if a.profiles.Get("PHONE_A").Usb.Baseline.Res != 2560 || a.profiles.Get("PHONE_B").Usb.Baseline.Res != 0 {
		t.Fatal("late main-cast specification crossed archives")
	}
	a.appWins[appWinKey(addr, "pkg")] = &appWinState{serial: addr, identity: "device:PHONE_A", pkg: "pkg"}
	a.onAppWinLine(addr, "pkg", line)
	if a.profiles.Get("PHONE_B").Usb.Baseline.Res != 0 {
		t.Fatal("late app-window specification crossed archives")
	}
	params := a.deviceLockParams("PHONE_A", devs)
	if params.ExpectedSerial != "PHONE_A" {
		t.Fatal("full serial pin was not passed to the cast script")
	}
}

func TestIdentityRestartAndParameterSaveKeepOriginalDevice(t *testing.T) {
	a, _ := multiTestApp()
	t.Cleanup(a.Close)
	devs := []adb.Device{identityPhone("PHONE_A"), identityPhone("PHONE_B")}
	a.profiles.SyncDevices(devs)
	setDevices(a, devs)
	addr := "192.0.2.10:5555"
	a.profiles.AddrSuccessMode("PHONE_A", addr, ModeTcpip)
	if err := a.StartCast(addr); err != nil {
		t.Fatal(err)
	}
	a.profiles.AddrSuccessMode("PHONE_B", addr, ModeTcpip)
	if err := a.SaveProfile(addr, "usb", 1920, 90, 20, true, "pc", false, false, "h264", "opus"); err != nil {
		t.Fatal(err)
	}
	if a.profiles.Get("PHONE_A").Usb.FPS != 90 || a.profiles.Get("PHONE_B").Usb.Custom {
		t.Fatal("session parameter save used the new address owner")
	}
	a.mu.Lock()
	a.sessions[addr].runner = nil
	a.sessions[addr].restarting = true
	a.mu.Unlock()
	if err := a.StartCast(addr); err != nil {
		t.Fatal(err)
	}
	if sessionBySerial(t, a, addr).Identity != "device:PHONE_A" {
		t.Fatal("restart selected a different device")
	}
}

func TestIdentityIconsIsolatedAndLegacyReadable(t *testing.T) {
	a, _ := multiTestApp()
	a.profiles.path = filepath.Join(t.TempDir(), "profiles.json")
	a.profiles.SyncDevices([]adb.Device{identityPhone("PHONE_A"), identityPhone("PHONE_B")})
	for _, serial := range []string{"PHONE_A", "PHONE_B"} {
		dir := a.iconsDirFor(a.profiles.ResolveKey(serial))
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "com.example.app.png"), []byte(serial), 0600); err != nil {
			t.Fatal(err)
		}
	}
	x, _ := a.GetAppIcon("PHONE_A", "com.example.app")
	y, _ := a.GetAppIcon("PHONE_B", "com.example.app")
	if x == "" || x == y {
		t.Fatal("same package reads cross-device icon")
	}
	if a.iconsDirFor("device:A:B") == a.iconsDirFor("device:A_B") || a.iconsDirFor("device:a") == a.iconsDirFor("device:A") {
		t.Fatal("directory names collide")
	}
	a.profiles.data.Devices["OLD PHONE"] = &DeviceEntry{Marketname: "OLD PHONE", Serials: []string{"OLD_SERIAL"}, Profiles: DefaultProfile()}
	dir := a.legacyIconsDirFor("OLD PHONE")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "com.example.app.png"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if icon, _ := a.GetAppIcon("OLD_SERIAL", "com.example.app"); icon == "" {
		t.Fatal("legacy icons unreadable")
	}
}

func TestIdentityAppWindowParametersAndPhysicalCacheSurviveAddressReuse(t *testing.T) {
	a, _ := multiTestApp()
	t.Cleanup(a.Close)
	devs := []adb.Device{identityPhone("PHONE_A"), identityPhone("PHONE_B")}
	a.profiles.SyncDevices(devs)
	setDevices(a, devs)
	addr := "192.0.2.10:5555"
	a.profiles.AddrSuccessMode("PHONE_A", addr, ModeTcpip)
	a.physCache["device:PHONE_A"] = devPhys{longSide: 3200, dpi: 600, at: time.Now()}
	a.physCache["device:PHONE_B"] = devPhys{longSide: 2400, dpi: 400, at: time.Now()}
	a.appWins[appWinKey(addr, "pkg")] = &appWinState{serial: addr, identity: "device:PHONE_A", pkg: "pkg", restarting: true}
	a.profiles.AddrSuccessMode("PHONE_B", addr, ModeTcpip)
	if err := a.SaveAppWinParams(addr, "pkg", `{"mode":"usb","size":"1920","fps":90,"bitrate":20,"dpi":360}`); err != nil {
		t.Fatal(err)
	}
	eA, _ := a.profiles.Entry("PHONE_A")
	eB, _ := a.profiles.Entry("PHONE_B")
	if eA.AppParams["pkg"].Usb.FPS != 90 || len(eB.AppParams) != 0 {
		t.Fatal("window save crossed archives")
	}
	v, err := a.GetAppWinParams(addr, "pkg")
	if err != nil || v.Usb.FPS != 90 || v.PhysDpi != 600 {
		t.Fatalf("window read used a new address owner: %+v %v", v, err)
	}
	if p := a.devicePhysCached(addr); p.dpi != 400 {
		t.Fatalf("new owner reused old physical cache: %+v", p)
	}
}

func TestIdentityPendingDeviceOrderIsNotPersisted(t *testing.T) {
	s := NewProfileStore(filepath.Join(t.TempDir(), "profiles.json"))
	if err := s.SetDeviceOrder([]string{"device:PHONE_A", "pending:192.0.2.10:5555"}); err != nil {
		t.Fatal(err)
	}
	if got := s.DeviceOrder(); len(got) != 1 || got[0] != "device:PHONE_A" {
		t.Fatal("pending identity was persisted in order", got)
	}
}

func TestIdentityDelayedIconPullCannotOverwriteOriginalCache(t *testing.T) {
	a, _ := multiTestApp()
	a.profiles.path = filepath.Join(t.TempDir(), "profiles.json")
	a.profiles.SyncDevices([]adb.Device{identityPhone("PHONE_A"), identityPhone("PHONE_B")})
	addr := "192.0.2.10:5555"
	a.profiles.AddrSuccessMode("PHONE_A", addr, ModeTcpip)
	dir := a.iconsDirFor("device:PHONE_A")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	icon := filepath.Join(dir, "pkg.png")
	if err := os.WriteFile(icon, []byte("original A"), 0600); err != nil {
		t.Fatal(err)
	}
	stage, err := os.MkdirTemp(filepath.Dir(dir), ".export-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(stage) })
	if err := os.WriteFile(filepath.Join(stage, "pkg.png"), []byte("new B"), 0600); err != nil {
		t.Fatal(err)
	}
	a.profiles.AddrSuccessMode("PHONE_B", addr, ModeTcpip)
	if ok, err := a.commitAppIcons("device:PHONE_A", addr, stage, nil, nil); ok || err != nil {
		t.Fatal("late pull was accepted", ok, err)
	}
	if got, _ := os.ReadFile(icon); string(got) != "original A" {
		t.Fatal("original cache was changed")
	}
	if ok, err := a.commitAppIcons("device:PHONE_B", addr, stage, []string{"pkg"}, nil); !ok || err != nil {
		t.Fatal("current device cannot update its own cache", ok, err)
	}
}

func TestIdentityOrphanCleanupPreservesHistoricalSettings(t *testing.T) {
	s := NewProfileStore("")
	s.SyncDevices([]adb.Device{identityPhone("PHONE_A")})
	s.AddrSuccessMode("PHONE_A", "192.0.2.10:44444", ModeTls)
	p := DefaultProfile()
	p.Usb.Custom, p.Usb.FPS = true, 90
	key := "192.0.2.10:5555"
	s.data.Devices[key] = &DeviceEntry{Profiles: p, Addrs: []AddrEntry{{Addr: key, State: AddrStateActive}}}
	s.CleanOrphanIPPort()
	if s.data.Devices[key] == nil || s.data.Devices[key].Profiles.Usb.FPS != 90 {
		t.Fatal("historical address archive settings were deleted")
	}
}

func TestIdentityAppWindowsAcceptArchiveKeysAndRemainIndependent(t *testing.T) {
	a, _ := multiTestApp()
	t.Cleanup(a.Close)
	devs := []adb.Device{identityPhone("PHONE_A"), identityPhone("PHONE_B")}
	a.profiles.SyncDevices(devs)
	setDevices(a, devs)
	for _, identity := range []string{"device:PHONE_A", "device:PHONE_B"} {
		a.physCache[identity] = devPhys{longSide: 3200, dpi: 600, at: time.Now()}
		if err := a.StartAppWin(identity, "pkg", "same application"); err != nil {
			t.Fatal(identity, err)
		}
	}
	items := a.Snapshot().AppWins
	if len(items) != 2 || items[0].Identity == items[1].Identity {
		t.Fatalf("archive-key window controls share a session: %+v", items)
	}
}

func BenchmarkIdentityStableSync(b *testing.B) {
	for _, count := range []int{1, 10, 100} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			s := NewProfileStore("")
			devs := make([]adb.Device, count)
			for i := range devs {
				devs[i] = identityPhone(fmt.Sprintf("BENCH_%03d", i))
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
