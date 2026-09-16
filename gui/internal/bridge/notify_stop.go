package bridge

import (
	"os"
	"path/filepath"
	"strings"
)

// gui54：设备端「正在投屏」通知在 GUI 退出后滞留的修复支撑（纯逻辑部分，跨平台可单测）。
//
// 滞留根因（真机实测 + events buffer 证据）：
// Stop 历史第一步是 taskkill /F /T <cmd pid>，把 scrcpy.exe 与它派生的 adb.exe
// 客户端同帧杀掉 → 启动 scrcpy-server 的那条 adb shell 会话断开 → 设备端 adbd 向
// app_process(server) 发 SIGHUP → server 被信号杀死、不走 finally
// （bgNotification.stop() → nm.cancel() 没执行）→ 通知永久滞留。
// 对照：只杀 scrcpy.exe（adb.exe 存活）时，server 经数据 socket EOF 优雅退出，
// 通知正常撤下（notification_canceled reason=8）。
//
// 修复：先单独关掉本会话客户端并等设备端通知撤下，再做整树清理；本文提供
// 判定与标记路径等纯函数，Windows 侧的进程/ adb 操作见 bat_windows.go。

// shellNotifyPkg 是设备端通知的归属包：scrcpy-server 以 shell(uid 2000) 身份运行，
// 通知挂在 com.android.shell 下。stop 时据此从 `cmd notification list` 里挑出
// 本会话需要等撤下的通知。
const shellNotifyPkg = "com.android.shell"

// shellNotificationKeys 从 `cmd notification list` 输出中取出 shell 包的通知 key。
//
// 输出每行形如：
//
//	0|com.android.shell|5456730|null|2000
//
// cmd notification list 只列活跃通知（不会命中渠道定义那种长驻噪音），这里再限定
// |com.android.shell|，避免把别的 App 的通知算进基线。
func shellNotificationKeys(out string) []string {
	var keys []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" {
			continue
		}
		if strings.Contains(line, "|"+shellNotifyPkg+"|") {
			keys = append(keys, line)
		}
	}
	return keys
}

// notificationsGone 判定基线里的通知 key 是否已全部从当前列表中消失。
func notificationsGone(current string, baseline []string) bool {
	for _, k := range baseline {
		if k == "" {
			continue
		}
		if strings.Contains(current, k) {
			return false
		}
	}
	return true
}

// closedFlagName 是本会话 bat「复活门」标记的文件名。
//
// 投屏支持.bat 在每个重入口都有 `if exist "%TEMP%\scrcpy_closed_<WATCH_TAG>.flag"
// exit /b 0`：存在即直接退出，不再自动重连。Stop 主动写它，保证客户端即使是被
// 强杀（退出码非 0）也不会在 AUTO_RETRY_SEC(=2s) 后被 bat 重连拉起新会话——
// 新会话会重新挂通知，而紧随其后的整树杀又会让它滞留。
//
// 标记按 WATCH_TAG 命名（会话唯一），且 bat 启动时会自删，不会污染后续会话。
func closedFlagName(tag string) string {
	if tag == "" {
		return ""
	}
	return "scrcpy_closed_" + tag + ".flag"
}

// closedFlagPath 返回复活门标记的完整路径（%TEMP% 下）。
func closedFlagPath(tag string) string {
	name := closedFlagName(tag)
	if name == "" {
		return ""
	}
	return filepath.Join(os.TempDir(), name)
}
