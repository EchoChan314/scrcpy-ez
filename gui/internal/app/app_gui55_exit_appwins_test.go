package app

// v2.1.55：GUI 退出（BeginClose）必须把应用窗口（虚拟屏）会话一并停。
// 此前 BeginClose 只遍历 sessions（主投屏）——虚拟屏 bat 未被通知（防重连
// 标记不写），GUI 退出 kill adb server 后虚拟屏 bat 自动重连把窗口重新拉起
// （用户观感：GUI 都退了，虚拟屏窗口还在/又冒出来）。
// 覆盖：①主投屏+多应用窗口全停 ②无应用窗口时行为不变（回归）。

import (
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// 退出链：主投屏 + 两个应用窗口（同设备）→ BeginClose 后三个 runner 全 Stop。
func TestBeginCloseStopsAppWins(t *testing.T) {
	rec := &fakeRecorder{by: map[string][]*fakeRunner{}}
	a := New(Config{BatPath: `C:\x\投屏支持.bat`, AdbPath: `C:\x\adb.exe`, Version: "test"})
	a.SetRunnerFactory(func(serial string, onLine func(string), onExit func(int)) (Runner, error) {
		f := &fakeRunner{exitCode: -1}
		rec.add(serial, f)
		return f, nil
	})
	setDevices(a, []adb.Device{{Serial: "S1", State: "device", ConnType: "usb", Name: "X", Identity: "X"}})
	a.physMu.Lock()
	a.physCache["S1"] = devPhys{longSide: 3200, dpi: 600, at: time.Now()}
	a.physMu.Unlock()

	if err := a.StartCast("S1"); err != nil {
		t.Fatal(err)
	}
	if err := a.StartAppWin("S1", "pkg.one", "一"); err != nil {
		t.Fatal(err)
	}
	if err := a.StartAppWin("S1", "pkg.two", "二"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for rec.count("S1") < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("等待 3 个 runner 超时（当前 %d）", rec.count("S1"))
		}
		time.Sleep(10 * time.Millisecond)
	}

	<-a.BeginClose() // 阻塞至全部 Stop 返回

	got := rec.serial("S1")
	if len(got) != 3 {
		t.Fatalf("应有 3 个 runner（主投屏+2 应用窗口），got %d", len(got))
	}
	for i, f := range got {
		if !f.isStopped() {
			t.Fatalf("runner[%d] 未被 Stop——GUI 退出必须停全部会话（主投屏+应用窗口）", i)
		}
	}
}

// 回归：无应用窗口时 BeginClose 只停主投屏（行为不变）。
func TestBeginCloseMainOnlyRegression(t *testing.T) {
	rec := &fakeRecorder{by: map[string][]*fakeRunner{}}
	a := New(Config{BatPath: `C:\x\投屏支持.bat`, AdbPath: `C:\x\adb.exe`, Version: "test"})
	a.SetRunnerFactory(func(serial string, onLine func(string), onExit func(int)) (Runner, error) {
		f := &fakeRunner{exitCode: -1}
		rec.add(serial, f)
		return f, nil
	})
	setDevices(a, []adb.Device{{Serial: "S2", State: "device", ConnType: "usb", Name: "Y", Identity: "Y"}})
	if err := a.StartCast("S2"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for rec.count("S2") < 1 {
		if time.Now().After(deadline) {
			t.Fatal("等待 runner 超时")
		}
		time.Sleep(10 * time.Millisecond)
	}
	<-a.BeginClose()
	if !rec.serial("S2")[0].isStopped() {
		t.Fatal("主投屏 runner 未被 Stop")
	}
}
