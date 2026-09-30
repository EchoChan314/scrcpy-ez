//go:build !windows

package app

import "os/exec"

// setupHiddenProc 非 Windows 桩（仅保证包可编译；真实实现见 appwin_spawn_windows.go）。
func setupHiddenProc(cmd *exec.Cmd) {}
