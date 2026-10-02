//go:build windows

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"scrcpy-ez/gui/internal/adb"
	"testing"
)

// Full-serial matching moved from BAT to castsupervisor; its cases live there.
// The parameter-only child must fail closed if no supervisor selected a route.
func TestEventBatchRequiresSupervisorRoute(t *testing.T) {
	path, err := filepath.Abs(filepath.Join("..", "..", "..", "packaging", "投屏启动.bat"))
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command("cmd.exe", "/d", "/c", path)
	c.Env = append(os.Environ(), "SCEZ_EVENT_CHILD=1", "SCEZ_EVENT_ROUTE=")
	adb.HideConsole(c)
	err = c.Run()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Fatalf("unselected child: %v", err)
	}
}
