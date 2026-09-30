//go:build windows

package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--ez-test-parent" {
		time.Sleep(90 * time.Second)
		os.Exit(0)
	}
	if RunIfRequested(os.Args[1:]) {
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestRecoveryRecordValidation(t *testing.T) {
	p := testPlan(t)
	j, e := Apply(p)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = recoveryJournal(p); e != nil {
		t.Fatal(e)
	}
	j.Changes = append(j.Changes, Change{Name: "profiles.json", Existed: true})
	WriteJSON(p.JournalPath(), j)
	if _, e = recoveryJournal(p); e == nil {
		t.Fatal("protected recovery path accepted")
	}
}
func TestInstallLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lock")
	a, e := lockFile(p)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if b, e := lockFile(p); e == nil {
		b.Close()
		t.Fatal("second lock allowed")
	}
}

func TestLockedRuntimeRollback(t *testing.T) {
	p := testPlan(t)
	name := p.Files[0].Name
	dest := filepath.Join(p.Install, name)
	write(t, dest, []byte("old-locked"))
	u, _ := windows.UTF16PtrFromString(dest)
	h, e := windows.CreateFile(u, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer windows.CloseHandle(h)
	j, e := Apply(p)
	if e == nil {
		t.Fatal("locked runtime was overwritten")
	}
	if e = Rollback(j); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(dest)
	if string(b) != "old-locked" {
		t.Fatal("locked file changed")
	}
}

// 真实 GUI / 公开运行库在临时目录启动，禁用真实 ADB，结束后仅关闭测试实例。
func TestWindowsWorkerEndToEnd(t *testing.T) {
	gui := os.Getenv("SCEZ_UPDATE_GUI")
	archive := os.Getenv("SCEZ_PUBLIC_PACKAGE")
	if gui == "" || archive == "" {
		t.Skip("real worker test is opt-in")
	}
	for _, scenario := range []string{"install", "rollback", "recovery", "legacy-public"} {
		t.Run(scenario, func(t *testing.T) { runWorkerScenario(t, gui, archive, scenario) })
	}
}

func runWorkerScenario(t *testing.T, gui, archive, scenario string) {
	p := testPlan(t)
	// 清除合成 stage，以真实公开包的全部运行库验证。
	if e := os.RemoveAll(p.Stage()); e != nil {
		t.Fatal(e)
	}
	os.MkdirAll(p.Stage(), 0700)
	var e error
	p.Files, e = Extract(context.Background(), archive, p.Stage())
	if e != nil {
		t.Fatal(e)
	}
	if scenario != "legacy-public" {
		os.Remove(filepath.Join(p.Stage(), "scrcpy-ez.exe"))
		if scenario == "rollback" {
			write(t, filepath.Join(p.Stage(), "scrcpy-ez.exe"), testPE(2, 3, 0))
		} else if e = copyFile(gui, filepath.Join(p.Stage(), "scrcpy-ez.exe")); e != nil {
			t.Fatal(e)
		}
	}
	p.Version, e = FileVersion(filepath.Join(p.Stage(), "scrcpy-ez.exe"))
	if e != nil {
		t.Fatal(e)
	}
	for i, f := range p.Files {
		if f.Name == "scrcpy-ez.exe" {
			h, n, e := fileHash(filepath.Join(p.Stage(), f.Name))
			if e != nil {
				t.Fatal(e)
			}
			p.Files[i] = File{Name: f.Name, Size: n, SHA256: h}
		}
	}
	// 旧文件均为哨兵，校验不会漏替换；用户档案保持独立。
	for _, f := range p.Files {
		write(t, filepath.Join(p.Install, f.Name), []byte("old-runtime"))
	}
	if scenario == "rollback" || scenario == "recovery" {
		os.Remove(filepath.Join(p.Install, "scrcpy-ez.exe"))
		if e = copyFile(gui, filepath.Join(p.Install, "scrcpy-ez.exe")); e != nil {
			t.Fatal(e)
		}
	}
	write(t, filepath.Join(p.Install, "profiles.json"), []byte("{\"devices\":{}}"))
	write(t, filepath.Join(p.Install, "settings.json"), []byte("{\"closeToTray\":false}"))
	write(t, filepath.Join(p.Install, "config.txt"), []byte("user-memory"))
	t.Setenv("SCEZ_ADB_PATH", filepath.Join(p.Install, "disabled-adb.exe"))
	t.Setenv("SCEZ_PROFILES_PATH", filepath.Join(p.Install, "profiles.json"))
	t.Setenv("SCEZ_CONFIG_PATH", filepath.Join(p.Install, "config.txt"))
	t.Setenv("SCEZ_BAT_PATH", filepath.Join(p.Install, "投屏支持.bat"))
	exe, _ := os.Executable()
	parent := exec.Command(exe, "--ez-test-parent")
	parent.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if e = parent.Start(); e != nil {
		t.Fatal(e)
	}
	defer parent.Process.Kill()
	p.ParentPID = parent.Process.Pid
	helper := filepath.Join(p.Work, "updater.exe")
	if e = copyFile(exe, helper); e != nil {
		t.Fatal(e)
	}
	planPath := filepath.Join(p.Work, "plan.json")
	WriteJSON(planPath, p)
	mode := "--ez-apply-update"
	if scenario == "recovery" {
		// 模拟安装提交后、健康确认前更新器中断，保留 durable journal。
		if _, e = Apply(p); e != nil {
			t.Fatal(e)
		}
		mode = "--ez-recover-update"
	}
	cmd := exec.Command(helper, mode, planPath, p.Token)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer cmd.Process.Kill()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	until := time.Now().Add(15 * time.Second)
	ack := false
	for time.Now().Before(until) {
		var a struct {
			Token string `json:"token"`
			Error string `json:"error"`
		}
		if ReadJSON(filepath.Join(p.Work, "started.json"), &a) == nil {
			if a.Error != "" {
				t.Fatal(a.Error)
			}
			ack = a.Token == p.Token
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ack {
		t.Fatal("helper ack timeout")
	}
	if _, e := os.Stat(p.ResultPath()); e == nil {
		t.Fatal("installed before parent exit")
	}
	parent.Process.Kill()
	parent.Wait()
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(85 * time.Second):
		t.Fatal("worker timeout")
	}
	// 只向拥有当前测试路径的 PID 发送 WM_CLOSE。
	closeTestGUI(t, p.Install)
	var r Result
	wantOK := scenario == "install" || scenario == "legacy-public"
	if e = ReadJSON(p.ResultPath(), &r); e != nil || r.OK != wantOK {
		t.Fatalf("result %+v %v", r, e)
	}
	if _, e = os.Stat(p.Backup()); !os.IsNotExist(e) {
		t.Fatal("backup not finalized")
	}
	for _, f := range p.Files {
		h, _, e := fileHash(filepath.Join(p.Install, f.Name))
		wantHash := f.SHA256
		if !wantOK {
			if f.Name == "scrcpy-ez.exe" {
				wantHash, _, _ = fileHash(gui)
			} else {
				sum := sha256.Sum256([]byte("old-runtime"))
				wantHash = hex.EncodeToString(sum[:])
			}
		}
		if e != nil || h != wantHash {
			t.Fatal("installed content", f.Name, e)
		}
	}
	for name, want := range map[string]string{"profiles.json": "{\"devices\":{}}", "settings.json": "{\"closeToTray\":false}", "config.txt": "user-memory"} {
		b, _ := os.ReadFile(filepath.Join(p.Install, name))
		if string(b) != want {
			t.Fatal("user data changed", name)
		}
	}
	t.Logf("%s %s: parent wait, %d runtime files, restart and backup cleanup passed", scenario, p.Version, len(p.Files))
}
func closeTestGUI(t *testing.T, install string) {
	t.Helper()
	// 获取进程可执行路径再比较，避免触及用户正式实例。
	want := filepath.Join(install, "scrcpy-ez.exe")
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var pid uint32
		user32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
		if e != nil {
			return 1
		}
		defer windows.CloseHandle(h)
		buf := make([]uint16, 32768)
		n := uint32(len(buf))
		if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil || !strings.EqualFold(windows.UTF16ToString(buf[:n]), want) {
			return 1
		}
		user32.NewProc("PostMessageW").Call(hwnd, 0x10, 0, 0)
		status, _ := windows.WaitForSingleObject(h, 20000)
		if status != windows.WAIT_OBJECT_0 {
			t.Error(fmt.Sprintf("test GUI did not close: %d", pid))
		}
		return 1
	})
	user32.NewProc("EnumWindows").Call(cb, 0)
}
