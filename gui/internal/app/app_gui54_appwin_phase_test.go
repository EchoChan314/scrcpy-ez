package app

// v2.1.54：应用窗口卡片动态状态文字（插拔转换/断线重连 → 与主投屏同款阶段文字）。
// 背景：卡片副行原先恒为"正在窗口"，插拔切换/重连期间看不出状态（主人反馈截图）。
// 覆盖：①映射表（转换类=主投屏同款措辞；投屏继续类=清空信号）
// ②生命周期（切换→显示；重连→更新；开始投屏→清空；虚拟屏→显示；纹理→清空）。

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
)

// 映射表：转换/重连类事件 → 主投屏 phaseText 同款措辞；
// 投屏继续类（casting/spec/watch-on/texture）=不设文字（清空回默认）。
func TestAppWinPhaseTextMapping(t *testing.T) {
	cases := []struct {
		kind bridge.Kind
		want string
		ok   bool
	}{
		// 转换/重连类：与主投屏 phaseText 措辞一致
		{bridge.KindSwitchUSB, "检测到 USB 插线，切换有线投屏…", true},
		{bridge.KindReconnect, "连接断开，自动重连中…", true},
		{bridge.KindADBReset, "正在准备 adb…", true},
		{bridge.KindDetect, "正在扫描设备…", true},
		{bridge.KindUSBFound, "已找到 USB 设备", true},
		{bridge.KindUSBHint, "检测到 USB 设备未授权，请解锁手机点击允许", true},
		{bridge.KindNoUSB, "未检测到 USB 设备，尝试无线连接", true},
		{bridge.KindWifiTry, "尝试连接上次的无线设备…", true},
		{bridge.KindWifiOK, "无线设备已连接", true},
		{bridge.KindWifiFail, "无线连接失败，正在扫描其他设备…", true},
		{bridge.KindLearning, "正在学习无线信息（约 15 秒）…", true},
		// 应用窗口特有：虚拟屏启动步骤（server push → 建虚拟屏 → 拉起应用）
		{bridge.KindVDCreating, "正在启动虚拟屏…", true},
		// 投屏继续类：不设文字（由 appWinPhaseClear 清空）
		{bridge.KindCasting, "", false},
		{bridge.KindSpec, "", false},
		{bridge.KindWatchOn, "", false},
		{bridge.KindTexture, "", false},
	}
	for _, tc := range cases {
		got, ok := appWinPhaseText(tc.kind)
		if ok != tc.ok || got != tc.want {
			t.Errorf("appWinPhaseText(%v) = (%q,%v)，期望 (%q,%v)", tc.kind, got, ok, tc.want, tc.ok)
		}
	}

	// 清除集=四种"投屏继续"信号；转换类不得进清除集（否则卡片会瞬间闪回默认）。
	for _, k := range []bridge.Kind{bridge.KindCasting, bridge.KindSpec, bridge.KindWatchOn, bridge.KindTexture} {
		if !appWinPhaseClear(k) {
			t.Errorf("appWinPhaseClear(%v) 应为 true", k)
		}
	}
	for _, k := range []bridge.Kind{bridge.KindSwitchUSB, bridge.KindReconnect, bridge.KindVDCreating, bridge.KindWifiTry} {
		if appWinPhaseClear(k) {
			t.Errorf("appWinPhaseClear(%v) 应为 false（转换类）", k)
		}
	}
}

// 生命周期：真实 bat 行序列走一遍（用日志里的原样行）。
func TestAppWinPhaseLifecycle(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb"}})
	if err := e.a.StartAppWin("12345TESTA", "com.android.browser", "浏览器"); err != nil {
		t.Fatal(err)
	}
	e.f.waitStarts(t, 1)

	// 初始：无状态（前端默认"正在窗口"）。
	if list := waitAppWins(t, e.a, 1); list[0].PhaseText != "" || list[0].Phase != "" {
		t.Fatalf("初始不应有状态文字: %+v", list[0])
	}

	// ① 插线切换行（bat 原样）→ 显示主投屏同款文字。
	e.fireLine("[自动切换] 检测到 USB 插线，切换至有线投屏...")
	if list := waitAppWins(t, e.a, 1); list[0].PhaseText != "检测到 USB 插线，切换有线投屏…" || list[0].Phase != "switch-usb" {
		t.Fatalf("切换中状态文字不符: %+v", list[0])
	}

	// ② 断线重连行 → 更新文字。
	e.fireLine("[提示] 检测到连接断开（退出码 2），2 秒后自动重连...")
	if list := waitAppWins(t, e.a, 1); list[0].PhaseText != "连接断开，自动重连中…" || list[0].Phase != "reconnect" {
		t.Fatalf("重连中状态文字不符: %+v", list[0])
	}

	// ③ 开始投屏（转换走完）→ 清空（回默认"正在窗口"）。
	e.fireLine("===== 开始投屏：Xiaomi Pad 8 Pro（TESTUSB0003） =====")
	if list := waitAppWins(t, e.a, 1); list[0].PhaseText != "" || list[0].Phase != "" {
		t.Fatalf("开始投屏后应清空状态: %+v", list[0])
	}

	// ④ 虚拟屏启动步骤（应用窗口特有；日志原样行）→ 显示。
	e.fireLine("[窗口] 虚拟屏 1920x1280 dpi=264 flex=1 静音= 应用=+com.android.browser")
	if list := waitAppWins(t, e.a, 1); list[0].PhaseText != "正在启动虚拟屏…" || list[0].Phase != "vd-creating" {
		t.Fatalf("虚拟屏启动状态文字不符: %+v", list[0])
	}

	// ⑤ 纹理就绪（画面出现）→ 清空。
	e.fireLine("INFO: Texture: 1920x1280")
	if list := waitAppWins(t, e.a, 1); list[0].PhaseText != "" || list[0].Phase != "" {
		t.Fatalf("纹理就绪后应清空状态: %+v", list[0])
	}

	// ⑥ 虚拟屏参数缺失回退行（异常路径）不得被当"正在启动虚拟屏"。
	e.fireLine("[窗口] 虚拟屏参数缺失（SCEZ_VD_SIZE[_USB/_WIFI] 均未定义，跳过虚拟屏参数）")
	if list := waitAppWins(t, e.a, 1); list[0].PhaseText != "" {
		t.Fatalf("参数缺失回退行不应设状态: %+v", list[0])
	}
}
