// 二期 Step 1c（图标导出）：adb 原语——push / shell / pull。
// 由 app 层（appwindow.go runAppIcons）编排成完整流程：
//   push server → 起 server（export_app_icons=true）→ pull 图标目录 → 入库。
// 所有 exec 均套 HideConsole（GUI 无控制台，防闪窗——与 adb 包其他调用一致）。

package adb

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sync"
)

// ShellOutStream reports complete lines while collecting a bounded response.
// It is intentionally separate from ShellOut: ordinary ADB operations keep
// their existing behavior. The callback must be fast and must not do I/O.
func (m *Manager) ShellOutStream(ctx context.Context, serial, command string, maxBytes int, onLine func(string)) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := &appOutputWriter{limit: maxBytes, onLine: onLine, cancel: cancel}
	c := exec.CommandContext(ctx, m.adbPath, "-s", serial, "shell", command)
	HideConsole(c)
	c.Stdout, c.Stderr = w, w
	err := c.Run()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending.Len() > 0 && onLine != nil {
		onLine(w.pending.String())
	}
	if w.overflow {
		return w.all.String(), errors.New("应用读取输出超过资源上限")
	}
	return w.all.String(), err
}

type appOutputWriter struct {
	mu           sync.Mutex
	all, pending bytes.Buffer
	limit        int
	overflow     bool
	onLine       func(string)
	cancel       context.CancelFunc
}

func (w *appOutputWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.all.Len()+len(p) > w.limit {
		w.overflow = true
		w.cancel()
		return 0, errors.New("app output limit")
	}
	w.all.Write(p)
	w.pending.Write(p)
	for {
		data := w.pending.Bytes()
		end := bytes.IndexByte(data, '\n')
		if end < 0 {
			break
		}
		line := string(bytes.TrimSuffix(data[:end], []byte{'\r'}))
		w.pending.Next(end + 1)
		if w.onLine != nil {
			w.onLine(line)
		}
	}
	return len(p), nil
}

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
