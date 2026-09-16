package bridge

import "fmt"

// ModeParams 是单模式（usb/wifi）的参数浮窗覆盖。
// Set=false 表示该模式自动档：不注入该模式环境变量，bat 该模式走自身检测逻辑。
// Set=true 时 GUI 注入该模式的三个环境变量（如 usb → SCEZ_RES_USB/SCEZ_FPS_USB/
// SCEZ_BITRATE_USB），bat 在 scrcpy 启动前用这三个值覆盖该模式检测值
// （长边/fps/码率 Mbps）。
type ModeParams struct {
	Res     int
	FPS     int
	Bitrate int
	Set     bool
}

// CastParams 投屏参数覆盖（多设备 Phase 1 轮 A 起扩展为四类注入）：
//   - Usb.Set=true → 注入 SCEZ_RES_USB/SCEZ_FPS_USB/SCEZ_BITRATE_USB（bat 有线分支覆盖）；
//   - Wifi.Set=true → 注入 SCEZ_RES_WIFI/SCEZ_FPS_WIFI/SCEZ_BITRATE_WIFI（bat 无线分支覆盖）；
//   - Serial 非空 → 注入 SCEZ_SERIAL（锁定目标设备：USB serial 或 IP:port）；
//   - Addr 非空 → 注入 SCEZ_ADDR（锁定主无线地址，跳过 config 记忆/扫描）；
//   - Addr2 非空 → 注入 SCEZ_ADDR2（备用无线地址，与主地址不同形态；bat 单次降级用）；
//   - NoWatch=true → 注入 SCEZ_NO_WATCH=1（已废弃：watcher 会话化后不再需要，
//     字段保留仅用于 bat 兼容——GUI 不再生成该值，恒 false）；
//   - Market/Model 非空 → 注入 SCEZ_MARKET/SCEZ_MODEL（档案身份：bat 无线回退防抢
//     比对与 watcher 比对本尊用；仅锁定会话注入）；
//   - OverlayVisibleSet=true → 注入 SCEZ_PARAM_OVERLAY="1"/"0"（设置面板开关 A：
//     启动投屏时投屏窗口上的参数控件默认显示/隐藏；不设该字段=不注入，客户端
//     按其自身默认（显示），与旧行为一致）。
//
// 未设置（空/零值）的字段不注入，bat 走原逻辑（回归兼容）。
type CastParams struct {
	Usb     ModeParams
	Wifi    ModeParams
	Serial  string
	Addr    string
	Addr2   string // SCEZ_ADDR2（备用无线地址；空=无备用）
	NoWatch bool
	Market  string // SCEZ_MARKET（档案 marketname，仅锁定会话）
	Model   string // SCEZ_MODEL（档案 model，仅锁定会话）
	// OverlayVisible 是参数控件（fps/码率/状态浮层）的启动可见性，仅当
	// OverlayVisibleSet=true 时注入（true→"1" 显示 / false→"0" 隐藏）。
	OverlayVisible    bool
	OverlayVisibleSet bool
}

// castEnv 组装注入给 bat 的环境变量表（GUI→bat→scrcpy.exe 的整条注入链）。
// 平台无关的纯函数：bat_windows.go 的 Start 直接用它，Linux 侧单测也直接覆盖
// （Windows 专属的启动/隐藏窗口逻辑不参与）。返回项均为 "K=V"：
//   - SCEZ_NO_ADB_RESET=1：GUI 已接管 adb 生命周期（track 长连/设备档案/插线学习），
//     GUI 启动的一切投屏 bat 一律跳过 adb kill-server 重置与清理，避免插线设备被
//     清掉、双长连被断。bat 独立运行时无此变量，保留原自愈逻辑；
//   - 各可选参数（未设置/零值不产生条目，bat 走原逻辑）；
//   - SCEZ_WATCH_TAG：本会话 watcher 唯一标记（多会话隔离，恒注入）。
func castEnv(params CastParams, watchTag string) []string {
	env := []string{"SCEZ_NO_ADB_RESET=1"}
	if params.Usb.Set {
		env = append(env,
			fmt.Sprintf("SCEZ_RES_USB=%d", params.Usb.Res),
			fmt.Sprintf("SCEZ_FPS_USB=%d", params.Usb.FPS),
			fmt.Sprintf("SCEZ_BITRATE_USB=%d", params.Usb.Bitrate))
	}
	if params.Wifi.Set {
		env = append(env,
			fmt.Sprintf("SCEZ_RES_WIFI=%d", params.Wifi.Res),
			fmt.Sprintf("SCEZ_FPS_WIFI=%d", params.Wifi.FPS),
			fmt.Sprintf("SCEZ_BITRATE_WIFI=%d", params.Wifi.Bitrate))
	}
	if params.Serial != "" {
		env = append(env, "SCEZ_SERIAL="+params.Serial)
	}
	if params.Addr != "" {
		env = append(env, "SCEZ_ADDR="+params.Addr)
	}
	if params.Addr2 != "" {
		env = append(env, "SCEZ_ADDR2="+params.Addr2)
	}
	if params.NoWatch {
		env = append(env, "SCEZ_NO_WATCH=1")
	}
	if params.Market != "" {
		env = append(env, "SCEZ_MARKET="+params.Market)
	}
	if params.Model != "" {
		env = append(env, "SCEZ_MODEL="+params.Model)
	}
	// 开关 A（设置面板）：参数控件启动可见性。bat 不解析该变量——它随 cmd.exe
	// 进程树被 scrcpy.exe 继承，客户端浮层初始化时读（1=显示，0=隐藏）。
	if params.OverlayVisibleSet {
		v := "0"
		if params.OverlayVisible {
			v = "1"
		}
		env = append(env, "SCEZ_PARAM_OVERLAY="+v)
	}
	env = append(env, "SCEZ_WATCH_TAG="+watchTag)
	return env
}
