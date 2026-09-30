package app

// v2.1.75「设备级单通知」配套测试：停止/重启会话前的"是否本设备最后一个会话"
// 判定（决定是否跳过"等设备端通知撤下"）。覆盖：
//   ① identity 归一（USB serial/无线地址/identity 三形态互认，跨设备不误判）；
//   ② 已受理停止（stopping/closing）的会话不算持有者（防"互以为对方持有"双双跳过
//      等待→双双被强杀→通知滞留的竞态）；重启中的算持有者（马上拉起新 server 接管）；
//   ③ StopCast/StopAppWin 的注入值（双会话→skip；最后会话→不 skip）。

import (
	"path/filepath"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// newNotifScopeApp：多 runner 工厂（每次创建独立 fake，按创建顺序记录）——
// 双会话场景需要主投屏/应用窗口各一个 runner。
func newNotifScopeApp(t *testing.T) (*App, *[]*fakeRunner) {
	t.Helper()
	runners := &[]*fakeRunner{}
	a := New(Config{BatPath: `C:\x\投屏支持.bat`, AdbPath: `C:\x\adb.exe`,
		Version: "test", ProfilesPath: filepath.Join(t.TempDir(), "profiles.json")})
	a.SetRunnerFactory(func(string, func(string), func(int)) (Runner, error) {
		f := &fakeRunner{exitCode: -1}
		*runners = append(*runners, f)
		return f, nil
	})
	return a, runners
}

// ① identity 归一：USB serial / IP:port / identity 三形态互认；跨设备不误判。
func TestAnyOnSameDeviceNormalization(t *testing.T) {
	a, _ := newNotifScopeApp(t)
	setDevices(a, []adb.Device{
		{Serial: "K80USB", Wireless: "192.168.1.5:5555", Identity: "K80", State: "device"},
		{Serial: "PadUSB", Identity: "Pad", State: "device"},
	})
	if !a.anyOnSameDevice("K80", []string{"192.168.1.5:5555"}) {
		t.Fatal("同设备（identity ↔ 无线地址）应识别为同一台")
	}
	if !a.anyOnSameDevice("K80USB", []string{"K80"}) {
		t.Fatal("同设备（USB serial ↔ identity）应识别为同一台")
	}
	if a.anyOnSameDevice("K80", []string{"PadUSB"}) {
		t.Fatal("不同设备不应识别为同一台")
	}
	if a.anyOnSameDevice("K80", nil) {
		t.Fatal("无其它会话应为 false")
	}
}

// ② 已受理停止（stopping/closing）的会话不算持有者；重启中的算。
func TestOtherSessionIDsStoppingNotHolder(t *testing.T) {
	a, _ := newNotifScopeApp(t)
	r1 := &fakeRunner{exitCode: -1}
	r2 := &fakeRunner{exitCode: -1}
	a.mu.Lock()
	a.sessions["K80"] = &sessionState{runner: r1}
	a.appWins[appWinKey("K80", "pkg.a")] = &appWinState{serial: "K80", pkg: "pkg.a", runner: r2}
	a.mu.Unlock()

	// 应用窗口稳定运行 → 是持有者（skipSessionKey="K80" 排除主投屏自己）。
	a.mu.Lock()
	got := a.otherSessionIDsLocked("K80", "")
	a.mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("窗口应为持有者: %v", got)
	}

	// 窗口已受理停止（closing）→ 不算持有者。
	a.mu.Lock()
	a.appWins[appWinKey("K80", "pkg.a")].closing = true
	got = a.otherSessionIDsLocked("K80", "")
	a.mu.Unlock()
	if len(got) != 0 {
		t.Fatalf("已受理停止的窗口不应算持有者: %v", got)
	}

	// 重启中（closing+restarting）→ 算持有者（马上拉起新 server 接管通知）。
	a.mu.Lock()
	a.appWins[appWinKey("K80", "pkg.a")].restarting = true
	got = a.otherSessionIDsLocked("K80", "")
	a.mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("重启中的窗口应为持有者: %v", got)
	}
}

// ③ 端到端：双会话（主投屏 + 应用窗口）——停应用窗口 → skip=true（主投屏持有）；
// 再停主投屏（窗口已受理停止=不算持有者）→ skip=false（它是"最后会话"，保持等待）。
func TestStopSkipInjectionDoubleSession(t *testing.T) {
	a, runners := newNotifScopeApp(t)
	setDevices(a, []adb.Device{{Serial: "K80", State: "device", Identity: "K80"}})
	if err := a.StartCast("K80"); err != nil {
		t.Fatal(err)
	}
	if err := a.StartAppWin("K80", "pkg.a", "A"); err != nil {
		t.Fatal(err)
	}
	if len(*runners) != 2 {
		t.Fatalf("应有 2 个 runner: %d", len(*runners))
	}
	castR, winR := (*runners)[0], (*runners)[1]

	// 停应用窗口：主投屏还在（稳定）→ 窗口 skip=true（跳过等待，通知由主投屏持有）。
	if err := a.StopAppWin("K80", "pkg.a"); err != nil {
		t.Fatal(err)
	}
	if !winR.lastSkipNotifWait() {
		t.Fatal("停应用窗口应 skip=true（主投屏为持有者）")
	}

	// 停主投屏：窗口已受理停止（不算持有者）→ 主投屏 skip=false（最后会话，保持等待）。
	if err := a.StopCast("K80"); err != nil {
		t.Fatal(err)
	}
	if castR.lastSkipNotifWait() {
		t.Fatal("主投屏应是最后会话 skip=false（保持等待，防滞留）")
	}
}

// ④ 孤立会话（单设备单会话）→ skip=false（默认等待路径不变）。
func TestStopIsolatedSessionNoSkip(t *testing.T) {
	a, runners := newNotifScopeApp(t)
	if err := a.StartCast("X"); err != nil {
		t.Fatal(err)
	}
	r := (*runners)[0]
	if err := a.StopCast("X"); err != nil {
		t.Fatal(err)
	}
	if r.lastSkipNotifWait() {
		t.Fatal("孤立会话应 skip=false（等待通知撤下，防滞留）")
	}
}

// ⑤ 跨设备：K80 主投屏 + Pad 应用窗口 → 停 K80 主投屏 skip=false（Pad 不算同设备持有者）。
func TestStopCrossDeviceNoSkip(t *testing.T) {
	a, runners := newNotifScopeApp(t)
	setDevices(a, []adb.Device{
		{Serial: "K80", State: "device", Identity: "K80"},
		{Serial: "Pad", State: "device", Identity: "Pad"},
	})
	if err := a.StartCast("K80"); err != nil {
		t.Fatal(err)
	}
	if err := a.StartAppWin("Pad", "pkg.p", "P"); err != nil {
		t.Fatal(err)
	}
	if err := a.StopCast("K80"); err != nil {
		t.Fatal(err)
	}
	if (*runners)[0].lastSkipNotifWait() {
		t.Fatal("跨设备不应算持有者：K80 主投屏 skip=false")
	}
}
