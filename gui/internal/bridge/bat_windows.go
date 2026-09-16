//go:build windows

package bridge

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"syscall"

	"golang.org/x/sys/windows"
)

// syscallProcAttr 缩短 syscall.SysProcAttr 的引用（Windows 专属字段）。
type syscallProcAttr = syscall.SysProcAttr

// BatRunner 隐藏启动 投屏支持.bat 并桥接其 stdin/stdout/stderr。
// 窗口隐藏：CreationFlags=CREATE_NO_WINDOW|CREATE_NEW_PROCESS_GROUP + HideWindow(SW_HIDE)。
// 输出：stdout/stderr 管道逐行读取 → GBK 解码 → onLine 回调。
// 输入：仅当 bat 出现 choice/pause 提示（app 层判定）才写 stdin。
type BatRunner struct {
	batPath string
	adbPath string
	onLine  func(string)
	onExit  func(code int)

	// watchTag 是本会话 watcher 的唯一标记（Start 时生成，SCEZ_WATCH_TAG 注入 bat，
	// bat 切换 flag 与 :stop_usb_watch 都按它区分——多会话互不误杀/互不误读）。
	// serialCandidates 是本会话 scrcpy 的 --serial 匹配候选（会话键/SCEZ_SERIAL/
	// SCEZ_ADDR 并集，Start 时定格）——Stop 残余 scrcpy 兜底复查按它判定"本会话的"，
	// 不按进程名全局误杀其他会话投屏。
	// CanKillServer 保留兼容占位（gui46 起 Stop 不再兜底 kill-server，此回调不再使用；
	// 字段/SetCanKillServer 仍保留以兼容外部调用方，不影响新行为）。
	watchTag         string
	serialCandidates []string
	CanKillServer    func() bool

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	mu     sync.Mutex
	code   int
	exited bool
}

func NewBatRunner(batPath, adbPath string, onLine func(string), onExit func(int)) *BatRunner {
	return &BatRunner{batPath: batPath, adbPath: adbPath, onLine: onLine, onExit: onExit, code: -1}
}

// Start 启动隐藏的 cmd.exe /c bat 子进程。
// 参数按模式分离注入：Usb.Set=true 注入 SCEZ_RES_USB/SCEZ_FPS_USB/SCEZ_BITRATE_USB，
// Wifi.Set=true 注入 SCEZ_RES_WIFI/SCEZ_FPS_WIFI/SCEZ_BITRATE_WIFI；
// Serial/Addr 非空注入 SCEZ_SERIAL/SCEZ_ADDR（锁定目标设备），
// NoWatch=true 注入 SCEZ_NO_WATCH=1（已废弃：GUI 不再生成——watcher 会话化后
// 并行会话各自 watcher 互不干扰；bat 侧门保留仅用于手工/旧调用兼容）；
// OverlayVisibleSet=true 注入 SCEZ_PARAM_OVERLAY=1/0（参数控件启动可见性）。
// 未自定义/未设置的字段不注入（bat 原逻辑）。
func (r *BatRunner) Start(serial string, params CastParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil {
		return errors.New("bat 已在运行")
	}
	// 本会话 scrcpy --serial 匹配候选（Stop 残余兜底复查用，Start 时定格）
	r.serialCandidates = serialCandidates(serial, params)

	DebugLog("[start] cmd.exe /c %s (usb set=%v res=%d fps=%d bitrate=%d | wifi set=%v res=%d fps=%d bitrate=%d | serial=%q addr=%q addr2=%q nowatch=%v | overlay set=%v visible=%v)",
		r.batPath, params.Usb.Set, params.Usb.Res, params.Usb.FPS, params.Usb.Bitrate,
		params.Wifi.Set, params.Wifi.Res, params.Wifi.FPS, params.Wifi.Bitrate,
		params.Serial, params.Addr, params.Addr2, params.NoWatch,
		params.OverlayVisibleSet, params.OverlayVisible)
	cmd := exec.Command("cmd.exe", "/c", r.batPath)
	// 多会话 watcher 隔离：每会话唯一 tag（bat 未注入时用基标）
	r.watchTag = fmt.Sprintf("%s_%d", WatchTag, time.Now().UnixNano())
	// 注入环境变量（含 SCEZ_PARAM_OVERLAY 等）——组装逻辑在 params.go 的 castEnv，
	// 平台无关、可单测（见 params_test.go）
	env := castEnv(params, r.watchTag)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Dir = filepath.Dir(r.batPath)
	cmd.SysProcAttr = &syscallProcAttr{
		CmdLine:       `cmd.exe /c "` + r.batPath + `"`,
		CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		DebugLog("[start] StdinPipe 失败: %v", err)
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		DebugLog("[start] StdoutPipe 失败: %v", err)
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		DebugLog("[start] StderrPipe 失败: %v", err)
		return err
	}

	if err := cmd.Start(); err != nil {
		DebugLog("[start] 启动失败: %v", err)
		return err
	}
	DebugLog("[start] 进程已启动 pid=%d", cmd.Process.Pid)
	r.cmd = cmd
	r.stdin = stdin
	r.code = -1
	r.exited = false

	go r.readLoop(stdout, "out")
	go r.readLoop(stderr, "err")
	go r.waitLoop()
	return nil
}

func (r *BatRunner) readLoop(rc io.ReadCloser, tag string) {
	defer rc.Close()
	br := bufio.NewReader(rc)
	n := 0
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			n++
			// 原始 GBK 解码后的行（调试日志含行号+时间，供卡死取证）
			DebugLog("[%s:%d] %s", tag, n, strings.TrimRight(line, "\r\n"))
			r.onLine(DecodeGBK([]byte(line)))
		}
		if err != nil {
			DebugLog("[%s] 管道 EOF（err=%v，共 %d 行）", tag, err, n)
			return
		}
	}
}

func (r *BatRunner) waitLoop() {
	err := r.cmd.Wait()
	code := -1
	if err == nil {
		code = 0
	} else if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	DebugLog("[exit] bat 进程退出 code=%d (wait err=%v)", code, err)
	r.mu.Lock()
	r.code = code
	r.exited = true
	r.mu.Unlock()
	r.onExit(code)
}

// 只读展示架构（主人决策）：GUI 不向 bat 写 stdin。stdin 管道仍创建并保持打开
// （choice 有有效句柄时按 /t 超时 /d 默认自愈；死等菜单由用户在 GUI 点会话级按钮，
// 经 app.RestartCast/StopCast 杀树处理），stdin 写端仅 Stop 时关闭。

// Stop 按固定顺序停止会话（stopSteps 契约，修复"双投屏"）：
//
//	⓪ gui54 通知滞留修复：先只收本会话 scrcpy 客户端（adb.exe 保持存活）并等设备端
//	   「正在投屏」通知撤下，再做整树清理。原因：① 里 taskkill /F /T 会把 scrcpy 与
//	   它派生的 adb.exe 同帧杀掉 → 启动 server 的 adb shell 会话断开 → adbd 向
//	   app_process(server) 发 SIGHUP → server 被信号杀死、不走 finally →
//	   bgNotification.stop()/nm.cancel() 没执行 → 手机通知永久滞留（主人 09-16 复现）。
//	   只杀客户端时 server 经数据 socket EOF 优雅退出，通知正常撤下。
//	   注：⓪ 不改变 ①~⑤ 的既有顺序与语义——cmd 全程存活，① 的整树杀/防逃逸兜底照旧，
//	   ⓪ 只是在它之前把客户端"请出去"，让 server 有机会自己撤通知。
//
//	① 先 taskkill /F /T <cmd pid> —— cmd 存活时整树杀（cmd+scrcpy+watcher 全灭，
//	   scrcpy 不会因父进程先死而逃逸成孤儿——历史 bug 是先 Process.Kill 只杀 cmd、
//	   再 taskkill 时 cmd 已死 → exit 128 → scrcpy 逃逸 → 保存重投后双窗口）；
//	② 再 Process.Kill 兜底（taskkill 失败/未完全退出时确保 cmd 主进程死）；
//	③ 按 WATCH_TAG 补杀 bat `start /b` 独立拉起的 watcher powershell
//	   （bat 被强杀时 :stop_usb_watch 无机会执行——否则残留 watcher 会在停止后
//	   继续写 flag 关 scrcpy，导致"停止后仍在重启"）；
//	④ 等 500ms 后复查本会话 scrcpy：父链=本 cmd pid 或命令行含本会话 serial 的
//	   残余进程 → taskkill /F 补杀（防 taskkill /T 128 失败后的孤儿窗口；
//	   只按会话判定，绝不误杀其他会话的投屏）；
//	⑤ 杀服门：仅当无其他活动会话才兜底 kill-server（共享 adb server 多会话保护）。
func (r *BatRunner) Stop() error {
	r.mu.Lock()
	cmd := r.cmd
	if r.stdin != nil {
		_ = r.stdin.Close()
		r.stdin = nil
	}
	tag := r.watchTag
	serials := append([]string{}, r.serialCandidates...)
	r.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return errors.New("bat 未在运行")
	}
	pid := cmd.Process.Pid
	DebugLog("[stop] 停止投屏 pid=%d（顺序：%s）", pid, strings.Join(stopSteps, "→"))

	// ⓪ gui54 通知滞留修复：先单独收掉本会话客户端（保留 adb.exe），等设备端通知撤下
	//    再做整树清理。写"复活门"标记在前：客户端若被强杀（退出码非 0），bat 也不会
	//    在 2 秒后自动重连拉起新会话（新会话会重新挂通知，随后整树杀又让它滞留）。
	markSessionClosed(tag)
	r.stopClientFirst(pid, serials)

	// ① 整树杀优先：cmd 存活时 taskkill /T 把 cmd+scrcpy+watcher 一并清掉
	treeKiller := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
	treeKiller.SysProcAttr = &syscallProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	if err := treeKiller.Run(); err != nil {
		DebugLog("[stop] taskkill /T 失败: %v（cmd 可能已退出，转 Kill+残余兜底）", err)
	}

	// ② Kill 兜底：无论 taskkill 是否成功，确保 cmd 主进程死亡（幂等；已死则报错忽略）
	if err := cmd.Process.Kill(); err != nil {
		DebugLog("[stop] Kill 兜底: %v（cmd 已退出=正常）", err)
	}

	// ③ 补杀独立 watcher（按本会话 tag 精确匹配，防串线死循环残留；
	// 未 Start 过/空 tag 退回基标兼容）
	DebugLog("[stop] 按 WATCH_TAG(%s) 清理残留 watcher", tag)
	watchKiller := exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden",
		"-Command", stopWatchPSCmd(tag))
	watchKiller.SysProcAttr = &syscallProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	if err := watchKiller.Run(); err != nil {
		DebugLog("[stop] watcher 清理失败: %v", err)
	}

	// ④ 残余 scrcpy 兜底复查（500ms 后）：只杀本会话的（父链/命令行判定）
	time.Sleep(500 * time.Millisecond)
	r.killResidualScrcpy(pid, serials)

	// ⑤ gui46：不再兜底 kill-server（原继承 bat :adb_cleanup 语义，杀服会断全部
	// transport 引发离线卡窗口）——adb 清理由 GUI 退出时统一执行（见 App.ShouldKillServerOnExit）。
	// bat 独立运行场景由 bat 自己的 :adb_cleanup 负责（不变）。
	return nil
}

// listScrcpyProcs 枚举全部 scrcpy.exe 进程（pid/ppid/cmdline，powershell CIM）。
// 输出行格式 "pid|ppid|cmdline"（cmdline 含管道符的概率可忽略，SplitN 兜底）。
// 注意：powershell 是 console 程序，GUI（无控制台）直接启动会闪黑框——
// 必须带 SysProcAttr（CREATE_NO_WINDOW+HideWindow），-WindowStyle Hidden 单独不足以防弹窗。
// 命令带 6s 超时（实测正常 0.7-0.9s）：powershell 偶发卡死时不再让浮前点击无限挂起。
func listScrcpyProcs() ([]scrcpyProc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	psCmd := "Get-CimInstance Win32_Process | Where-Object { $_.Name -eq 'scrcpy.exe' } | " +
		"ForEach-Object { [string]$_.ProcessId + '|' + [string]$_.ParentProcessId + '|' + [string]$_.CommandLine }"
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-WindowStyle", "Hidden", "-Command", psCmd)
	cmd.SysProcAttr = &syscallProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseScrcpyProcs(string(out)), nil
}

// killResidualScrcpy 复查并补杀本会话残余 scrcpy（Stop ④）：
// 只按"父链==本 cmd pid 或 命令行含本会话 serial"判定本会话，多会话安全。
func (r *BatRunner) killResidualScrcpy(cmdPid int, serials []string) {
	procs, err := listScrcpyProcs()
	if err != nil {
		DebugLog("[stop] 残余 scrcpy 复查失败（跳过）: %v", err)
		return
	}
	for _, p := range residualScrcpyCandidates(procs, cmdPid, serials) {
		DebugLog("[stop] 兜底补杀本会话残余 scrcpy pid=%d (ppid=%d, cmdline=%.120s)", p.pid, p.ppid, p.cmdline)
		killer := exec.Command("taskkill", "/F", "/PID", strconv.Itoa(p.pid))
		killer.SysProcAttr = &syscallProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
		if err := killer.Run(); err != nil {
			DebugLog("[stop] 补杀 scrcpy pid=%d 失败: %v", p.pid, err)
		}
	}
}

// ExitCode 返回 bat 最终退出码；运行中返回 -1。
func (r *BatRunner) ExitCode() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.code
}

// SetCanKillServer 设置 Stop 兜底 kill-server 前的判定回调（App 注入）：
// 返回 true 才杀共享 adb server；false 跳过（其他会话仍在投屏）。
func (r *BatRunner) SetCanKillServer(f func() bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.CanKillServer = f
}


// ---------------------------------------------------------------- gui54：通知滞留修复

const (
	// stopClientExitWait 是等客户端响应"关窗"（WM_CLOSE → SDL_QUIT → 完整退出流程，
	// 含恢复设备屏幕状态/收尾写盘）的上限。窗口会话实测退出耗时 0.3~1.5s，取 2.5s
	// 保证它走完正常退出（bat 记 code=0，不触发重连分支）；超时只可能是无窗口会话
	// （--no-window），此时退化为强杀客户端单进程（仍保留 adb.exe）。
	stopClientExitWait = 2500 * time.Millisecond
	// stopNotifWaitMax 是等设备端通知撤下的上限。server 优雅退出耗时实测不稳定
	// （0.4s~1.6s+，取决于 opengl/socket 收尾），因此必须轮询而不是固定 sleep。
	stopNotifWaitMax = 4 * time.Second
	// stopNotifPollPeriod 是设备端通知轮询间隔。
	stopNotifPollPeriod = 200 * time.Millisecond
)

// markSessionClosed 写 bat 的"复活门"标记（best effort，失败不阻断停止）。
func markSessionClosed(tag string) {
	p := closedFlagPath(tag)
	if p == "" {
		return
	}
	if err := os.WriteFile(p, []byte("1"), 0o644); err != nil {
		DebugLog("[stop] 写关闭标记失败: %v", err)
		return
	}
	DebugLog("[stop] 已写关闭标记 %s（防 bat 自动重连）", filepath.Base(p))
}

// stopClientFirst 先单独收掉本会话的 scrcpy 客户端，并等设备端通知撤下。
//
// 为什么必须"先客户端、后进程树"（实测证据见 notify_stop.go 头注释）：
//   - taskkill /F /T 会把 scrcpy.exe 与它派生的 adb.exe 客户端同帧杀掉 →
//     启动 server 的 adb shell 会话断开 → adbd 向 app_process(server) 发 SIGHUP
//     → server 不走 finally → finally 里的 nm.cancel() 没执行 → 通知滞留；
//   - 只杀 scrcpy.exe（adb.exe 存活）时，server 通过数据 socket EOF 感知断开 →
//     正常退出 → finally 撤通知。
//
// 步骤：优雅关窗（taskkill /PID 不带 /F，窗口 WM_CLOSE）→ 必要时强杀客户端单进程
// （--no-window 场景）→ 轮询设备端通知确认撤下 → 交回原有整树清理（防逃逸语义不变）。
func (r *BatRunner) stopClientFirst(cmdPid int, serials []string) {
	procs, err := listScrcpyProcs()
	if err != nil {
		DebugLog("[stop] scrcpy 枚举失败，跳过优雅收尾: %v", err)
		return
	}
	targets := residualScrcpyCandidates(procs, cmdPid, serials)
	if len(targets) == 0 {
		DebugLog("[stop] 未发现本会话 scrcpy 客户端，跳过优雅收尾")
		return
	}
	// 关客户端之前先取设备端通知基线（客户端一死就取不到了）
	baseline := r.shellNotifKeys(serials)
	DebugLog("[stop] 优雅收尾：本会话客户端 %d 个，设备端基线通知 %d 条", len(targets), len(baseline))

	// ① 优雅关窗：taskkill /PID 不带 /F → 向窗口发 WM_CLOSE → scrcpy 走 SDL_QUIT
	//    → 退出码 0 + 打印 SCRCPY_EZ_USER_CLOSE（bat 走"窗口关闭"分支，不会重连）
	alive := make([]int, 0, len(targets))
	for _, p := range targets {
		gracefulClosePID(p.pid)
		alive = append(alive, p.pid)
	}
	// ② 等客户端退出；仍存活（--no-window 没有窗口可关）→ 强杀客户端单进程
	deadline := time.Now().Add(stopClientExitWait)
	for len(alive) > 0 && time.Now().Before(deadline) {
		alive = alivePIDs(alive)
		if len(alive) == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(alive) > 0 {
		DebugLog("[stop] 客户端未响应关窗（无窗口会话），强杀单进程 %v（保留 adb.exe）", alive)
		for _, pid := range alive {
			forceKillPID(pid)
		}
	}

	// ③ 等设备端通知撤下：server 经数据 socket EOF 优雅退出后再做整树清理
	if len(baseline) > 0 {
		gone, waited := r.waitShellNotifGone(serials, baseline)
		DebugLog("[stop] 设备端通知已撤下=%v（等待 %s）", gone, waited.Round(time.Millisecond))
	}
}

// gracefulClosePID 向进程窗口发 WM_CLOSE（taskkill 不带 /F）。
func gracefulClosePID(pid int) {
	cmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid))
	cmd.SysProcAttr = &syscallProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	if err := cmd.Run(); err != nil {
		DebugLog("[stop] taskkill /PID %d（关窗）: %v", pid, err)
	}
}

// forceKillPID 强杀单个进程（不连带子进程：adb.exe 必须活着，server 才能优雅退出）。
func forceKillPID(pid int) {
	cmd := exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid))
	cmd.SysProcAttr = &syscallProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	if err := cmd.Run(); err != nil {
		DebugLog("[stop] taskkill /F /PID %d: %v", pid, err)
	}
}

// alivePIDs 用 tasklist 复查这些 pid 里还有哪些存活（无副作用，不会误发关窗）。
func alivePIDs(pids []int) []int {
	var alive []int
	for _, pid := range pids {
		cmd := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH")
		cmd.SysProcAttr = &syscallProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		if strings.Contains(string(out), strconv.Itoa(pid)) {
			alive = append(alive, pid)
		}
	}
	return alive
}

// shellNotifKeys 取设备端 com.android.shell 活跃通知 key 基线（失败返回 nil=不等待）。
func (r *BatRunner) shellNotifKeys(serials []string) []string {
	out, ok := r.adbShellOutput(serials, "cmd", "notification", "list")
	if !ok {
		return nil
	}
	return shellNotificationKeys(out)
}

// waitShellNotifGone 轮询设备端，直到基线通知全部消失（或超时）。
func (r *BatRunner) waitShellNotifGone(serials, baseline []string) (bool, time.Duration) {
	start := time.Now()
	for {
		if out, ok := r.adbShellOutput(serials, "cmd", "notification", "list"); ok {
			if notificationsGone(out, baseline) {
				return true, time.Since(start)
			}
		}
		if time.Since(start) >= stopNotifWaitMax {
			return false, time.Since(start)
		}
		time.Sleep(stopNotifPollPeriod)
	}
}

// adbShellOutput 用会话候选 serial 逐个尝试 `adb -s <serial> shell <args...>`，
// 返回首个成功的输出（候选=会话键/SCEZ_SERIAL/SCEZ_ADDR 并集，覆盖 USB serial 与
// 无线 host:port 两种形态）。全部失败返回 ok=false（调用方静默跳过等待）。
func (r *BatRunner) adbShellOutput(serials []string, args ...string) (string, bool) {
	if r.adbPath == "" {
		return "", false
	}
	for _, s := range serials {
		if s == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		argv := append([]string{"-s", s, "shell"}, args...)
		cmd := exec.CommandContext(ctx, r.adbPath, argv...)
		cmd.SysProcAttr = &syscallProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
		out, err := cmd.Output()
		cancel()
		if err == nil {
			return string(out), true
		}
		DebugLog("[stop] adb -s %s shell %s 失败: %v", s, strings.Join(args, " "), err)
	}
	return "", false
}
