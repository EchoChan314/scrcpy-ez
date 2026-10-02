package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/discovery"
)

// --- gui52fix1：配对 identity 归并 + 孤儿 IP:port 档案清理 ---

// TestGui55PairSerialUnknownRefusesArchive（原 gui52fix1「短号未知」用例改写）：
// gui55 起「配对必须学到名称」——短号学不到（mDNS 服务名 + get-serialno 都拿不到）
// 时明确报错（ErrCode=serial）且不入档，绝不静默写残缺档案、也不新建 IP:port 键。
func TestGui55PairSerialUnknownRefusesArchive(t *testing.T) {
	a, _ := newWirelessApp()
	// 已配对档案：serial + tlsGuid + 同 IP 的 5555/旧 TLS 地址
	gui15Seed(a.profiles, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Model:      "25091RP04C",
		Serials:    []string{"TEST0002"},
		TlsGuid:    "adb-TEST0002-KWqpio",
		Addrs: []AddrEntry{
			{Addr: "192.0.2.183:5555", State: AddrStateActive, Mode: ModeTcpip},
			{Addr: "192.0.2.183:40725", State: AddrStateActive, Mode: ModeTls},
		},
		Profiles: DefaultProfile(),
	})

	var mu sync.Mutex
	var conns []string
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		return "Successfully paired to " + ip + ":" + port, nil
	}
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		mu.Lock()
		conns = append(conns, addr)
		mu.Unlock()
		return "connected to " + addr, nil
	}
	a.pairOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		return "", errors.New("getprop unavailable") // marketname/man/model 全空
	}
	a.pairOps.mdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return nil, nil // 现场重扫也取不到服务名
	}
	// gui55：短号最硬来源（adb get-serialno）也读不到 → 走「明确报错不入档」分支。
	a.pairOps.serialFn = func(ctx context.Context, addr string) (string, error) {
		return "", errors.New("get-serialno unavailable")
	}

	if err := a.PairConnect("", "192.0.2.183", "37033", "38167", "123456"); err != nil {
		t.Fatal(err)
	}
	st := waitPairPhase(t, a, PairPhaseFailed)
	if st.ErrCode != PairErrSerial {
		t.Fatalf("短号学不到应分类 serial: %+v", st)
	}
	if st.ErrText == "" {
		t.Fatal("短号学不到必须给出可见文案（不得静默）")
	}

	// 不入档：既有档案不变，也不产生任何新档案/新地址。
	entries := a.profiles.Entries()
	if len(entries) != 1 {
		t.Fatalf("短号学不到时不得新建档案: %v", entries)
	}
	e, ok := entries[fixtureEntriesKey(entries, "Xiaomi Pad 8 Pro")]
	if !ok {
		t.Fatalf("主档案应保留: %v", entries)
	}
	if gui24FindAddr(e, "192.0.2.183:38167") != nil {
		t.Fatalf("短号学不到时不得写入新地址: %+v", e.Addrs)
	}
	if !contains(e.Serials, "TEST0002") {
		t.Fatalf("serials 不得丢失: %+v", e.Serials)
	}
}

// TestGui55PairClaimMergesIncompleteProfileByIP：残缺档案（无短号）在同 IP 上
// 重新配对 → 认领并入既有档案（写新地址 + 补短号自举），不新建第二张卡。
// 覆盖 K80 那类「只有一条 TLS 地址、serials/tlsGuid 全空」档案的自愈路径。
func TestGui55PairClaimMergesIncompleteProfileByIP(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Addrs: []AddrEntry{
			{Addr: "192.0.2.159:39377", State: AddrStateStale, Mode: ModeTls},
		},
		Profiles: DefaultProfile(),
	})
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		return "Successfully paired to " + ip + ":" + port, nil
	}
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		return "connected to " + addr, nil
	}
	a.pairOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		return "", errors.New("getprop unavailable") // marketname/man/model 全空：逼出认领路径
	}
	a.pairOps.serialFn = func(ctx context.Context, addr string) (string, error) {
		return "TEST0001", nil // 设备自报短号（get-serialno）
	}
	a.pairOps.mdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return nil, nil
	}

	if err := a.PairConnect("", "192.0.2.159", "37033", "41234", "123456"); err != nil {
		t.Fatal(err)
	}
	waitPairPhase(t, a, PairPhaseSuccess)

	entries := a.profiles.Entries()
	if len(entries) != 1 {
		t.Fatalf("残缺档案应被认领并入，不得新建第二档: %v", entries)
	}
	e, ok := entries[fixtureEntriesKey(entries, "REDMI K80")]
	if !ok {
		t.Fatalf("主档案应保留（按 IP 认领）: %v", entries)
	}
	if !contains(e.Serials, "TEST0001") {
		t.Fatalf("短号应补进档案（自举）: %+v", e.Serials)
	}
	tls := gui24FindAddr(e, "192.0.2.159:41234")
	if tls == nil || tls.State != AddrStateActive || tls.Mode != ModeTls {
		t.Fatalf("配对地址应写入既有档案 active: %+v", e.Addrs)
	}
}

// TestGui52Fix1PairKnownSerialBehaviorUnchanged：hintSerial 正常解析 → 仍走
// 既有 identity 路径归并，不新建 IP:port 键档案。
func TestGui52Fix1PairKnownSerialBehaviorUnchanged(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Model:      "25091RP04C",
		Serials:    []string{"TEST0002"},
		TlsGuid:    "adb-TEST0002-KWqpio",
		Addrs: []AddrEntry{
			{Addr: "192.0.2.183:5555", State: AddrStateActive, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})
	// 快照给出 TLS 服务名 → hintSerial=TEST0002
	a.mdnsMu.Lock()
	a.mdns = []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-TEST0002-Ab12Cd", Addr: "192.0.2.183:38167", Mode: discovery.MdnsModeTls},
	}
	a.mdnsMu.Unlock()

	var mu sync.Mutex
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		return "Successfully paired to " + ip + ":" + port, nil
	}
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		mu.Lock()
		mu.Unlock()
		return "connected to " + addr, nil
	}
	a.pairOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		switch prop {
		case "ro.product.marketname":
			return "Xiaomi Pad 8 Pro", nil
		case "ro.product.manufacturer":
			return "Xiaomi", nil
		case "ro.product.model":
			return "25091RP04C", nil
		}
		return "", nil
	}
	a.pairOps.mdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return nil, nil
	}

	if err := a.PairConnect("", "192.0.2.183", "37033", "38167", "123456"); err != nil {
		t.Fatal(err)
	}
	waitPairPhase(t, a, PairPhaseSuccess)

	waitFor(t, 3*time.Second, func() bool {
		e, ok := a.profiles.Entry(fixtureArchiveKey(a.profiles, "Xiaomi Pad 8 Pro"))
		return ok && gui50Fix45EntryHasAddr(e, "192.0.2.183:38167", ModeTls)
	}, "TLS 地址应归并完成")
	entries := a.profiles.Entries()
	if len(entries) != 1 {
		t.Fatalf("hintSerial 正常时行为不应变化: %v", entries)
	}
	if _, ok := entries[fixtureEntriesKey(entries, "192.0.2.183:38167")]; ok {
		t.Fatalf("不应出现 IP:port 键档案: %v", entries)
	}
	e, _ := a.profiles.Entry(fixtureArchiveKey(a.profiles, "Xiaomi Pad 8 Pro"))
	if e.TlsGuid != "adb-TEST0002-Ab12Cd" {
		t.Fatalf("tlsGuid 应更新: %+v", e)
	}
}

// TestGui52Fix1LoadCleansOrphanIPPortArchive：Load 时孤儿 IP:port 键并入
// 同 IP 主档案并删除；deviceOrder 同步清理；二次加载幂等。
func TestGui52Fix1LoadCleansOrphanIPPortArchive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	data := `{
  "devices": {
    "192.0.2.183:38167": {
      "addrs": [{"addr": "192.0.2.183:38167", "state": "active", "mode": "tls"}],
      "profiles": {"usb": {}, "wifi": {}}
    },
    "Xiaomi Pad 8 Pro": {
      "marketname": "Xiaomi Pad 8 Pro",
      "model": "25091RP04C",
      "serials": ["TEST0002"],
      "tlsGuid": "adb-TEST0002-KWqpio",
      "addrs": [
        {"addr": "192.0.2.183:5555", "state": "active", "mode": "tcpip"},
        {"addr": "192.0.2.183:40725", "state": "active", "mode": "tls"}
      ],
      "profiles": {"usb": {}, "wifi": {}}
    }
  },
  "deviceOrder": ["192.0.2.183:38167", "Xiaomi Pad 8 Pro"]
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewProfileStore(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	entries := s.Entries()
	if _, ok := entries[fixtureEntriesKey(entries, "192.0.2.183:38167")]; ok {
		t.Fatalf("孤儿 IP:port 键应被删除: %v", entries)
	}
	main, ok := entries[fixtureEntriesKey(entries, "Xiaomi Pad 8 Pro")]
	if !ok {
		t.Fatalf("主档案应保留: %v", entries)
	}
	if len(main.Addrs) != 2 {
		t.Fatalf("同 IP 孤儿应并入主档案（同形态折叠后 TLS+5555 各一条）: %+v", main.Addrs)
	}
	if !gui50Fix45EntryHasAddr(main, "192.0.2.183:5555", ModeTcpip) ||
		!gui50Fix45EntryHasAddr(main, "192.0.2.183:40725", ModeTls) {
		t.Fatalf("主档案真实证据应保留: %+v", main.Addrs)
	}
	if got := s.DeviceOrder(); len(got) != 1 || got[0] != "Xiaomi Pad 8 Pro" {
		t.Fatalf("deviceOrder 应移除孤儿键: %v", got)
	}

	// 幂等：二次加载无变化
	b1, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s2 := NewProfileStore(path)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	b2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("二次加载应幂等:\n---1---\n%s\n---2---\n%s", b1, b2)
	}
	raw := string(b2)
	for _, legacy := range []string{`"fail":`, `"lastOk":`, `"lastFail":`, `"stale":`} {
		if strings.Contains(raw, legacy) {
			t.Fatalf("落盘不得含旧字段 %s:\n%s", legacy, b2)
		}
	}
}

// TestGui52Fix1LoadKeepsOrphanWithoutSameIPMain：不同 IP → 孤儿保留（可能是
// 真正还没入档的设备，不能误删）。
func TestGui52Fix1LoadKeepsOrphanWithoutSameIPMain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	data := `{
  "devices": {
    "192.0.2.184:38167": {
      "addrs": [{"addr": "192.0.2.184:38167", "state": "active", "mode": "tls"}],
      "profiles": {"usb": {}, "wifi": {}}
    },
    "Xiaomi Pad 8 Pro": {
      "marketname": "Xiaomi Pad 8 Pro",
      "serials": ["TEST0002"],
      "addrs": [{"addr": "192.0.2.183:5555", "state": "active", "mode": "tcpip"}],
      "profiles": {"usb": {}, "wifi": {}}
    }
  }
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewProfileStore(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	entries := s.Entries()
	if _, ok := entries[fixtureEntriesKey(entries, "192.0.2.184:38167")]; !ok {
		t.Fatalf("无同 IP 主档案的孤儿应保留: %v", entries)
	}
	if len(entries) != 2 {
		t.Fatalf("不同 IP 不得误并: %v", entries)
	}
}

// TestGui52Fix1ResolveKeyByIPActivePriority：同 IP 多档案时 active 状态优先。
func TestGui52Fix1ResolveKeyByIPActivePriority(t *testing.T) {
	s := NewProfileStore("")
	s.mu.Lock()
	s.data.Devices["StaleDevice"] = &DeviceEntry{
		Addrs:    []AddrEntry{{Addr: "192.0.2.183:44444", State: AddrStateStale, Mode: ModeTls}},
		Profiles: DefaultProfile(),
	}
	s.data.Devices["ActiveDevice"] = &DeviceEntry{
		Addrs:    []AddrEntry{{Addr: "192.0.2.183:5555", State: AddrStateActive, Mode: ModeTcpip}},
		Profiles: DefaultProfile(),
	}
	s.mu.Unlock()

	if got := s.ResolveKeyByIP("192.0.2.183"); got != "ActiveDevice" {
		t.Fatalf("同 IP 应按 active 状态优先，got %q", got)
	}
	// 全 stale 时仍有确定性命中
	s.mu.Lock()
	s.data.Devices[fixtureArchiveKeyLocked(s, "ActiveDevice")].Addrs[0].State = AddrStateStale
	s.data.Devices[fixtureArchiveKeyLocked(s, "ActiveDevice")].Addrs[0].Stale = true
	s.mu.Unlock()
	if got := s.ResolveKeyByIP("192.0.2.183"); got == "" {
		t.Fatal("无 active 时 stale 档案仍应按 IP 命中")
	}
}
