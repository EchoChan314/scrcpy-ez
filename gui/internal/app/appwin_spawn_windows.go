//go:build windows

package app

import (
	"os/exec"
	"syscall"
)

// setupHiddenProc 隐藏控制台窗口（scrcpy 的 SDL 窗口不受影响）。
// CREATE_NO_WINDOW = 0x08000000（syscall 包无常量，直接给值）。
func setupHiddenProc(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
