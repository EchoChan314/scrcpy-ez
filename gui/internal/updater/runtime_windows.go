//go:build windows

package updater

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func lockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	var o windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &o); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func replaceFile(src, dst string) error {
	s, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	d, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(s, d, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func Launch(p Plan) error {
	return launch(p, false)
}
func launch(p Plan, recovery bool) error {
	p.ParentPID = os.Getpid()
	var validation error
	if recovery {
		_, validation = recoveryJournal(p)
	} else {
		validation = p.Validate()
	}
	if validation != nil {
		return validation
	}
	if _, err := os.Stat(p.Backup()); !recovery && !os.IsNotExist(err) {
		return fmt.Errorf("上次安装备份尚未处理，请先检查安装结果")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	helper := filepath.Join(p.Work, "updater.exe")
	os.Remove(helper)
	if err := copyFile(exe, helper); err != nil {
		return err
	}
	planPath := filepath.Join(p.Work, "plan.json")
	if err := WriteJSON(planPath, p); err != nil {
		return err
	}
	ack := filepath.Join(p.Work, "started.json")
	os.Remove(ack)
	mode := "--ez-apply-update"
	if recovery {
		mode = "--ez-recover-update"
	}
	cmd := exec.Command(helper, mode, planPath, p.Token)
	cmd.Dir = p.Install
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("无法启动更新器：%w", err)
	}
	go cmd.Wait()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var response struct {
			Token string `json:"token"`
			Error string `json:"error"`
		}
		if ReadJSON(ack, &response) == nil && response.Token == p.Token {
			if response.Error != "" {
				return fmt.Errorf("%s", response.Error)
			}
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	return fmt.Errorf("更新器未能就绪，旧程序继续运行")
}

// RunIfRequested 在 GUI 初始化前运行，更新器不启动 WebView、ADB 或托盘。
func RunIfRequested(args []string) bool {
	if len(args) == 0 || (args[0] != "--ez-apply-update" && args[0] != "--ez-recover-update") {
		return false
	}
	if len(args) != 3 {
		return true
	}
	var p Plan
	if ReadJSON(args[1], &p) != nil || p.Token != args[2] {
		return true
	}
	exe, err := os.Executable()
	if err != nil || !strings.EqualFold(exe, filepath.Join(p.Work, "updater.exe")) || args[1] != filepath.Join(p.Work, "plan.json") || p.ParentPID <= 0 {
		return true
	}
	ack := struct {
		Token string `json:"token"`
		Error string `json:"error"`
	}{Token: p.Token}
	parent, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(p.ParentPID))
	if err == nil {
		defer windows.CloseHandle(parent)
	}
	if err != nil && err != windows.ERROR_INVALID_PARAMETER {
		ack.Error = "无法等待旧程序退出"
	}
	recovery := args[0] == "--ez-recover-update"
	var validation error
	if recovery {
		_, validation = recoveryJournal(p)
	} else {
		validation = p.Validate()
	}
	if e := validation; e != nil {
		ack.Error = e.Error()
	}
	if e := CheckWritable(p.Install); e != nil {
		ack.Error = e.Error()
	}
	if WriteJSON(filepath.Join(p.Work, "started.json"), ack) != nil || ack.Error != "" {
		return true
	}
	if parent != 0 {
		status, e := windows.WaitForSingleObject(parent, 60000)
		if e != nil || status != windows.WAIT_OBJECT_0 {
			_ = SaveResult(p, false, fmt.Errorf("旧程序尚未退出，更新未执行"))
			return true
		}
	}
	lock, err := lockFile(filepath.Join(p.Install, ".ez-update.lock"))
	if err != nil {
		_ = SaveResult(p, false, fmt.Errorf("另一更新任务正在安装"))
		return true
	}
	defer lock.Close()
	if recovery {
		j, e := recoveryJournal(p)
		if e == nil {
			e = Rollback(j)
		}
		if e != nil {
			_ = SaveResult(p, false, e)
			showUpdateFailure(e)
			return true
		}
		_ = SaveResult(p, false, fmt.Errorf("上次更新被中断，已恢复旧版，可再次安装"))
		_ = os.RemoveAll(p.Backup())
		_ = startGUI(p.Install, nil)
		return true
	}
	journal, err := Apply(p)
	if err == nil {
		err = startAndConfirm(p)
	}
	if err != nil {
		rollbackErr := Rollback(journal)
		if rollbackErr != nil {
			err = fmt.Errorf("%v；%v", err, rollbackErr)
		}
		_ = SaveResult(p, false, err)
		if rollbackErr == nil {
			_ = os.RemoveAll(p.Backup())
			_ = startGUI(p.Install, nil)
		} else {
			showUpdateFailure(err)
		}
		return true
	}
	_ = SaveResult(p, true, nil)
	_ = os.RemoveAll(p.Backup())
	// 成功后释放体积最大的包与暂存文件；正在运行的 helper 留待系统清理。
	if _, e := safePath(p.Work, "stage"); e == nil {
		_ = os.RemoveAll(p.Stage())
	}
	_ = os.Remove(filepath.Join(p.Work, "package.zip"))
	cachePath := filepath.Join(filepath.Dir(p.Work), "candidate.json")
	var c cached
	if ReadJSON(cachePath, &c) == nil && c.Plan.Token == p.Token && c.Plan.Work == p.Work {
		_ = os.Remove(cachePath)
	}
	return true
}

func showUpdateFailure(err error) {
	message, e := windows.UTF16PtrFromString("更新未完成，请保留备份并检查文件占用或磁盘空间。\n\n" + err.Error())
	if e != nil {
		return
	}
	title, _ := windows.UTF16PtrFromString("scrcpy-ez 更新失败")
	user32.NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
}

func recoveryJournal(p Plan) (Journal, error) {
	var j Journal
	if len(p.Token) != 32 || strings.Trim(p.Token, "0123456789abcdef") != "" || !filepath.IsAbs(p.Install) || filepath.Clean(p.Install) != p.Install || filepath.Dir(p.Install) == p.Install || !filepath.IsAbs(p.Work) {
		return j, fmt.Errorf("恢复计划无效")
	}
	if _, err := safePath(p.Install, ".ez-update-backup-"+p.Token+"/journal.json"); err != nil {
		return j, err
	}
	if err := ReadJSON(p.JournalPath(), &j); err != nil {
		return j, err
	}
	if j.Plan.Token != p.Token || j.Plan.Install != p.Install || j.Plan.Work != p.Work || len(j.Changes) > 4096 {
		return j, fmt.Errorf("恢复记录无效")
	}
	seen := map[string]bool{}
	for _, c := range j.Changes {
		if protected(c.Name) || seen[strings.ToLower(c.Name)] {
			return j, fmt.Errorf("恢复路径无效")
		}
		seen[strings.ToLower(c.Name)] = true
		if _, err := safePath(p.Install, c.Name); err != nil {
			return j, err
		}
		if _, err := safePath(filepath.Join(p.Backup(), "files"), c.Name); err != nil {
			return j, err
		}
	}
	return j, nil
}

// 正常新进程启动时安装锁仍由更新器持有；只有遗留事务才触发恢复。
func RecoverIfNeeded(install string) bool {
	entries, _ := filepath.Glob(filepath.Join(install, ".ez-update-backup-*"))
	if len(entries) == 0 {
		return false
	}
	lock, err := lockFile(filepath.Join(install, ".ez-update.lock"))
	if err != nil {
		return false
	}
	defer lock.Close()
	for _, path := range entries {
		var j Journal
		if ReadJSON(filepath.Join(path, "journal.json"), &j) != nil || j.Plan.Install != install || j.Plan.Backup() != path {
			continue
		}
		if err := launch(j.Plan, true); err != nil {
			_ = SaveResult(j.Plan, false, fmt.Errorf("无法自动恢复，请保留备份 %s：%w", path, err))
			return false
		}
		return true
	}
	return false
}

func startGUI(install string, args []string) error {
	cmd := exec.Command(filepath.Join(install, "scrcpy-ez.exe"), args...)
	cmd.Dir = install
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		return err
	}
	// 回滚重开同样恢复可见窗口，不让旧版停在隐藏窗口。
	for i := 0; i < 100; i++ {
		if showOwnWindow(uint32(cmd.Process.Pid)) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return cmd.Process.Release()
}

func startAndConfirm(p Plan) error {
	health := filepath.Join(p.Work, "healthy.json")
	os.Remove(health)
	cmd := exec.Command(filepath.Join(p.Install, "scrcpy-ez.exe"), "--ez-update-health", p.Work, p.Token)
	cmd.Dir = p.Install
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("新版启动失败：%w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			return fmt.Errorf("新版启动后退出，已恢复旧版")
		default:
		}
		var ready struct {
			Token   string `json:"token"`
			Version string `json:"version"`
		}
		if ReadJSON(health, &ready) == nil && ready.Token == p.Token {
			if strings.TrimPrefix(ready.Version, "v") != strings.TrimPrefix(p.Version, "v") {
				_ = cmd.Process.Kill()
				<-exited
				return fmt.Errorf("新版实际版本与发行版本不一致")
			}
			showOwnWindow(uint32(cmd.Process.Pid))
			return nil
		}
		// 旧公开包没有 UiReady 健康协议，仅用于隔离兼容测试；新版本严格等待协议。
		if VersionLess(p.Version, "v2.2.6") && showOwnWindow(uint32(cmd.Process.Pid)) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	<-exited
	return fmt.Errorf("新版未能完成启动，已恢复旧版")
}

var user32 = windows.NewLazySystemDLL("user32.dll")

func showOwnWindow(pid uint32) bool {
	found := false
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var owner uint32
		user32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&owner)))
		if owner != pid {
			return 1
		}
		buf := make([]uint16, 128)
		user32.NewProc("GetWindowTextW").Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if windows.UTF16ToString(buf) != "scrcpy-ez" {
			return 1
		}
		user32.NewProc("ShowWindow").Call(hwnd, 9)
		user32.NewProc("SetForegroundWindow").Call(hwnd)
		found = true
		return 0
	})
	user32.NewProc("EnumWindows").Call(cb, 0)
	return found
}

func NotifyHealthy(args []string, version string) {
	if len(args) != 3 || args[0] != "--ez-update-health" {
		return
	}
	var p Plan
	if ReadJSON(filepath.Join(args[1], "plan.json"), &p) != nil || p.Token != args[2] || p.Work != args[1] {
		return
	}
	exe, err := os.Executable()
	if err != nil || !strings.EqualFold(exe, filepath.Join(p.Install, "scrcpy-ez.exe")) {
		return
	}
	_ = WriteJSON(filepath.Join(p.Work, "healthy.json"), struct {
		Token   string `json:"token"`
		Version string `json:"version"`
	}{p.Token, version})
}
