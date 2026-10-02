//go:build !windows

package deviceevents

import "os/exec"

func hide(c *exec.Cmd) {}
