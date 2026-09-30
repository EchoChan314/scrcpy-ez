// 二期 Step 1c（图标导出）：adb 原语——push / shell / pull。
// 由 app 层（appwindow.go runAppIcons）编排成完整流程：
//   push server → 起 server（export_app_icons=true）→ pull 图标目录 → 入库。
// 所有 exec 均套 HideConsole（GUI 无控制台，防闪窗——与 adb 包其他调用一致）。

package adb

import (
	"context"
	"os/exec"
)

// PushFile 执行 `adb -s <serial> push <local> <remote>`。
func (m *Manager) PushFile(ctx context.Context, serial, local, remote string) error {
	c := exec.CommandContext(ctx, m.adbPath, "-s", serial, "push", local, remote)
	HideConsole(c)
	_, err := c.CombinedOutput()
	return err
}

// ShellOut 执行 `adb -s <serial> shell <cmd>`（返回合并输出，供文本判定）。
func (m *Manager) ShellOut(ctx context.Context, serial, shellCmd string) (string, error) {
	c := exec.CommandContext(ctx, m.adbPath, "-s", serial, "shell", shellCmd)
	HideConsole(c)
	out, err := c.CombinedOutput()
	return string(out), err
}

// PullDir 执行 `adb -s <serial> pull <remote> <local>`（remote 以 "/." 结尾=拉目录内容）。
func (m *Manager) PullDir(ctx context.Context, serial, remote, local string) error {
	c := exec.CommandContext(ctx, m.adbPath, "-s", serial, "pull", remote, local)
	HideConsole(c)
	_, err := c.CombinedOutput()
	return err
}

// PullFile 执行 `adb -s <serial> pull <remote> <local>`（单文件；v2.1.16 图标定向更新用）。
// remote/local 均为完整文件路径（非目录）。
func (m *Manager) PullFile(ctx context.Context, serial, remote, local string) error {
	c := exec.CommandContext(ctx, m.adbPath, "-s", serial, "pull", remote, local)
	HideConsole(c)
	_, err := c.CombinedOutput()
	return err
}
