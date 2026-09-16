//go:build windows

package ui

import (
	"context"
	"log"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/webview/webview_go"
	"golang.org/x/sys/windows"

	"scrcpy-ez/gui/internal/app"
	"scrcpy-ez/gui/internal/bridge"
)

// setWindowIcon gui53：把 exe 资源里嵌入的图标（ID=1，像素风）设为窗口图标，
// 任务栏 / Alt+Tab 即显示它（WebView2 默认不设窗口图标，任务栏显示通用蓝窗）。
// 说明：x/sys/windows 未导出 LoadIcon/SendMessage，用 LazyDLL 直调 user32。
func setWindowIcon(hwnd uintptr) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	procLoadIcon := user32.NewProc("LoadIconW")
	procSendMessage := user32.NewProc("SendMessageW")

	// MAKEINTRESOURCEW(1)：资源名=ID 1（go-winres 生成的 ICON 资源 id=1）
	hIcon, _, _ := procLoadIcon.Call(0, 1)
	if hIcon == 0 {
		return
	}
	// WM_SETICON == 0x0080；ICON_BIG=1（Alt+Tab/窗口大图标）、ICON_SMALL=0（标题栏/任务栏小图标）
	const wmSetIcon, iconBig, iconSmall = 0x0080, 1, 0
	procSendMessage.Call(hwnd, wmSetIcon, iconBig, hIcon)
	procSendMessage.Call(hwnd, wmSetIcon, iconSmall, hIcon)
}

// --- 关窗拦截（设置开关 B：关闭窗口时最小化到托盘） ---
//
// 主窗口由 webview 库自己创建（webview.h 里的窗口过程：WM_CLOSE → DestroyWindow
// → WM_DESTROY → 主循环收到 WM_QUIT 退出 → main 收尾退出进程）。要做到"点关闭
// 只隐藏窗口"，只能在窗口过程上挂钩子：子类化（SetWindowLongPtr GWLP_WNDPROC），
// WM_CLOSE 时按设置
//   - 开关 B=开：ShowWindow(SW_HIDE) 隐藏窗口并吞掉消息 —— 进程存活、正在进行的
//     投屏会话不中断；托盘「显示主窗口」用 SW_RESTORE 恢复；
//   - 开关 B=关：原样交还原窗口过程（默认销毁）→ 完整退出，与旧行为一致。
// 托盘「退出」与 JS ExitApp 走 Quit()：置强制退出位后向主窗口投递 WM_CLOSE，
// 由窗口过程在 UI 线程绕过"最小化到托盘"，交还原窗口过程销毁窗口（完整退出）。
const (
	wmClose        = 0x0010
	swHide         = 0
	gwlpWndProcIdx = ^uintptr(3) // GWLP_WNDPROC == -4（x/sys 未导出，直接按无符号传）
)

var (
	closeHookUser32       = windows.NewLazySystemDLL("user32.dll")
	procShowWindowW       = closeHookUser32.NewProc("ShowWindow")
	procCallWindowProcW   = closeHookUser32.NewProc("CallWindowProcW")
	procDefWindowProcW    = closeHookUser32.NewProc("DefWindowProcW")
	procSetWindowLongPtrW = closeHookUser32.NewProc("SetWindowLongPtrW")
	procPostMessageW      = closeHookUser32.NewProc("PostMessageW")

	closeHookMu   sync.Mutex
	closeHookPrev uintptr  // 原窗口过程（子类化前）
	closeHookHwnd uintptr  // 主窗口句柄（Quit 投递 WM_CLOSE 用）
	closeHookApp  *app.App // 读设置用（Settings().CloseToTray 每次 WM_CLOSE 实时取）
	// forceExit=强制退出（托盘「退出」/JS ExitApp）：置位后 WM_CLOSE 不再被
	// "最小化到托盘"拦截，走原窗口过程销毁窗口 → 进程完整退出。
	forceExit atomic.Bool
	// closeHookProc 必须常驻：syscall.NewCallback 返回的地址要一直有效
	closeHookProc = syscall.NewCallback(handleWindowMessage)
)

// handleWindowMessage 是主窗口的替换窗口过程：只关心 WM_CLOSE，其余原样转发。
// 注意：本回调运行在 UI 线程（WebView 主循环所在线程），Go runtime 支持外部线程
// 回调；内部只做 ShowWindow(SW_HIDE) + 读一次设置——设置存储自带锁，最坏情况等
// 一次毫秒级落盘，不做任何长耗时或等待消息的操作。
func handleWindowMessage(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	if msg == wmClose {
		closeHookMu.Lock()
		a := closeHookApp
		prev := closeHookPrev
		closeHookMu.Unlock()
		if !forceExit.Load() && a != nil && a.Settings().CloseToTray {
			// 隐藏而非销毁：进程与全部投屏会话保持运行
			procShowWindowW.Call(hwnd, swHide)
			bridge.DebugLog("[ui] 关闭窗口 → 最小化到托盘（开关 B 开；进程与投屏保持）")
			return 0
		}
		// 关闭最小化到托盘=关，或托盘「退出」/ExitApp 的强制退出：交还原窗口过程
		// （默认销毁窗口 → WM_DESTROY → 主循环退出 → 进程完整退出）
		if prev != 0 {
			r, _, _ := procCallWindowProcW.Call(prev, hwnd, uintptr(msg), wparam, lparam)
			return r
		}
	}
	closeHookMu.Lock()
	prev := closeHookPrev
	closeHookMu.Unlock()
	if prev != 0 {
		r, _, _ := procCallWindowProcW.Call(prev, hwnd, uintptr(msg), wparam, lparam)
		return r
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wparam, lparam)
	return r
}

// installCloseHook 子类化主窗口（幂等；失败只记日志——关窗行为退回默认完整退出，
// 不影响其余功能）。
func installCloseHook(hwnd uintptr, a *app.App) {
	if hwnd == 0 {
		return
	}
	closeHookMu.Lock()
	defer closeHookMu.Unlock()
	if closeHookPrev != 0 {
		return
	}
	prev, _, err := procSetWindowLongPtrW.Call(hwnd, gwlpWndProcIdx, closeHookProc)
	if prev == 0 {
		log.Printf("[ui] 关窗拦截安装失败（关窗行为保持默认：完整退出）: %v", err)
		return
	}
	closeHookPrev = prev
	closeHookHwnd = hwnd
	closeHookApp = a
	log.Printf("[ui] 关窗拦截已安装 hwnd=%#x closeToTray=%v", hwnd, a.Settings().CloseToTray)
	bridge.DebugLog("[ui] 关窗拦截已安装 hwnd=%#x closeToTray=%v", hwnd, a.Settings().CloseToTray)
}

// Quit 请求完整退出（托盘「退出」与 JS ExitApp 用）：置强制位后向主窗口投递
// WM_CLOSE。消息由本包窗口过程在 UI 线程处理：强制位打开 → 绕过"最小化到托盘"，
// 交还原窗口过程销毁窗口 → WM_DESTROY → 主循环退出 → ui.Run 返回 → main 统一收尾。
//
// 为什么不用 webview 的 Terminate()：它内部是 PostQuitMessage(0)，只对**调用线程**
// 的消息队列生效——从托盘 goroutine 调用会投到错误的线程，主循环不会退出
// （实测：Terminate 后进程仍在运行、窗口仍开着）。走 WM_CLOSE 天然在 UI 线程执行。
func Quit() {
	forceExit.Store(true)
	closeHookMu.Lock()
	hwnd := closeHookHwnd
	closeHookMu.Unlock()
	if hwnd != 0 {
		procPostMessageW.Call(hwnd, wmClose, 0, 0)
	}
}

// Run 创建 WebView 主窗口并阻塞运行主循环（必须由主 goroutine 调用）。
// 窗口关闭后返回；退出前会调用 app.Close 释放资源。
func Run(a *app.App, html string) error {
	w := webview.New(false)
	defer w.Destroy()

	w.SetTitle("scrcpy-ez")
	w.SetSize(520, 760, webview.HintNone)

	// gui53：主窗口图标（任务栏 / Alt+Tab 显示）——WebView2 默认不设置窗口图标，
	// 任务栏会显示通用蓝窗图标；这里把 exe 资源里嵌入的像素图标（ID=1）发给窗口。
	if hwnd := uintptr(w.Window()); hwnd != 0 {
		setWindowIcon(hwnd)
		// 设置开关 B：子类化主窗口，按设置决定"关闭最小化到托盘"还是完整退出
		installCloseHook(hwnd, a)
	}

	bindings := []struct {
		name string
		fn   interface{}
	}{
		// 读轮询接口（GetState 700ms / RefreshNow 手动刷新）不记日志：高频刷屏无来源价值。
		{"GetState", func() (interface{}, error) { return a.Snapshot(), nil }},
		{"RefreshNow", func() (interface{}, error) { a.RefreshNow(); return a.Snapshot(), nil }},
		{"ForceDiscover", func() (interface{}, error) { a.ForceDiscover(); return a.Snapshot(), nil }},
		// 全部变更类入口加 [js] 来源日志（StartCast 全链路来源追踪：90 秒自动重投
		// 现场——前端谁在调一目了然；下一行 [app] 层另有 caller 栈帧留痕）。
		{"StartCast", func(serial string) error {
			bridge.DebugLog("[js] StartCast serial=%q", serial)
			return a.StartCast(serial)
		}},
		// 轮 B 多会话：并行会话入口（新设备弹窗【开始投屏】）+ 会话级操作（全部带 serial）
		{"StartCastParallel", func(serial string) error {
			bridge.DebugLog("[js] StartCastParallel serial=%q", serial)
			return a.StartCastParallel(serial)
		}},
		{"StopCast", func(serial string) error {
			bridge.DebugLog("[js] StopCast serial=%q", serial)
			return a.StopCast(serial)
		}},
		{"RestartCast", func(serial string) error {
			bridge.DebugLog("[js] RestartCast serial=%q", serial)
			return a.RestartCast(serial)
		}},
		{"ResetCast", func() error {
			bridge.DebugLog("[js] ResetCast")
			a.ResetCast()
			return nil
		}},
		{"DismissNewDevice", func(serial string) error {
			bridge.DebugLog("[js] DismissNewDevice serial=%q", serial)
			a.DismissNewDevice(serial)
			return nil
		}},
		{"ForgetSession", func(serial string) error {
			bridge.DebugLog("[js] ForgetSession serial=%q", serial)
			a.ForgetSession(serial)
			return nil
		}},
		// 标签点击 → 对应投屏窗口浮前（不夺 ez 焦点；失败不阻塞前端标签切换）
		{"BringCastToFront", func(serial string) error {
			bridge.DebugLog("[js] BringCastToFront serial=%q", serial)
			return a.BringCastToFront(serial)
		}},
		{"GetProfile", func(serial string) (interface{}, error) {
			bridge.DebugLog("[js] GetProfile serial=%q", serial)
			return a.GetProfile(serial), nil
		}},
		{"SaveProfileAndRestart", func(serial, mode string, res, fps, bitrate int, custom bool) error {
			bridge.DebugLog("[js] SaveProfileAndRestart serial=%q mode=%s res=%d fps=%d bitrate=%d custom=%v",
				serial, mode, res, fps, bitrate, custom)
			return a.SaveProfileAndRestart(serial, mode, res, fps, bitrate, custom)
		}},
		// 设备参数管理页：仅保存 profiles.json（不重投）
		{"SaveProfile", func(serial, mode string, res, fps, bitrate int, custom bool) error {
			bridge.DebugLog("[js] SaveProfile serial=%q mode=%s res=%d fps=%d bitrate=%d custom=%v",
				serial, mode, res, fps, bitrate, custom)
			return a.SaveProfile(serial, mode, res, fps, bitrate, custom)
		}},
		// 无线调试配对向导（gui12）：受理即返回，过程/结果经快照 pairStatus 轮询。
		// devKey=待配对卡键（自动发现场景）；手动场景 ip/pairPort/connPort 直接给值。
		{"PairConnect", func(devKey, ip, pairPort, connPort, code string) error {
			bridge.DebugLog("[js] PairConnect devKey=%q ip=%q pairPort=%q connPort=%q", devKey, ip, pairPort, connPort)
			return a.PairConnect(devKey, ip, pairPort, connPort, code)
		}},
		{"PairReset", func() error {
			bridge.DebugLog("[js] PairReset")
			a.PairReset()
			return nil
		}},
		{"ExitApp", func() {
			bridge.DebugLog("[js] ExitApp")
			// gui53：先停全部投屏会话（杀树），再杀 adb server——顺序保证
			// kill-server 不断正在投屏的 bat（与 main 退出路径同口径）。
			a.Close()
			if adbPath, ok := a.ShouldKillServerOnExit(); ok && adbPath != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				cleaner := exec.CommandContext(ctx, adbPath, "kill-server")
				cleaner.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
				_ = cleaner.Run()
			}
			Quit()
		}},
		// 设置面板：两个开关写入全局设置并落盘（settings.json）。幂等全量写；
		// 开关 A 下一次启动投屏生效，开关 B 立即生效（关窗时实时读设置）。
		{"SetSettings", func(showParamOverlay, closeToTray bool) error {
			bridge.DebugLog("[js] SetSettings showParamOverlay=%v closeToTray=%v", showParamOverlay, closeToTray)
			return a.SetSettings(showParamOverlay, closeToTray)
		}},
		// 设备卡顺序写回（gui45：后端持久化；前端两处触发、后端幂等）
		{"SetDeviceOrder", func(order []string) error {
			bridge.DebugLog("[js] SetDeviceOrder n=%d", len(order))
			return a.SetDeviceOrder(order)
		}},
		// gui51 设备管理：右键/批量删除、批量改名（改名以 JSON 字符串传入）。
		{"DeleteDevices", func(keys []string) error {
			bridge.DebugLog("[js] DeleteDevices n=%d", len(keys))
			return a.DeleteDevices(keys)
		}},
		{"RenameDevices", func(raw string) error {
			bridge.DebugLog("[js] RenameDevices raw=%q", raw)
			return a.RenameDevicesJSON(raw)
		}},
		// UiReady 由页面 load 事件调用：此时主循环已起，可取 HWND
		{"UiReady", func() {
			bridge.DebugLog("[js] UiReady")
			if hwnd := w.Window(); hwnd != nil {
				SetHWND(hwnd)
			}
		}},
	}
	for _, b := range bindings {
		if err := w.Bind(b.name, b.fn); err != nil {
			log.Printf("[ui] 绑定 %s 失败: %v", b.name, err)
		}
	}

	w.SetHtml(html)
	w.Run()
	return nil
}

// --- 主窗口 HWND 注册表（供 systray"显示主窗口"使用） ---

var (
	hwndOnce    unsafe.Pointer
	hwndReady   = make(chan struct{}) // 页面就绪信号：close 一次，之后永久可读
	hwndSetOnce sync.Once
)

// SetHWND 由 UiReady 回调设置（UI 线程上调用）。
// 同时把宿主窗口句柄注入 bridge（标签点击二段式浮前的④"ez 顶回"用）。
func SetHWND(p unsafe.Pointer) {
	atomic.StorePointer(&hwndOnce, p)
	hwndSetOnce.Do(func() { close(hwndReady) })
	bridge.SetFrontEzHwnd(uintptr(p))
}

// WaitHWND 阻塞至页面就绪并返回主窗口句柄。
// 就绪后本函数可重复调用（close 过的信号 channel 立即返回 + 缓存值读取）：
// 旧实现用容量 1 的 channel 传值，每次调用消费一个值 —— 第二次调用即永久阻塞，
// 托盘「显示主窗口」自第二次起彻底失效（且该 goroutine 一并卡死）。
func WaitHWND() unsafe.Pointer {
	<-hwndReady
	return atomic.LoadPointer(&hwndOnce)
}
