package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"scrcpy-ez/gui/internal/updater"
)

// 安装用写锁等待正在启动的投屏；各投屏用读锁，仍可并行启动。
type appUpdater struct {
	once       sync.Once
	manager    *updater.Manager
	mu         sync.RWMutex
	installing bool
}

func (a *App) updates() *updater.Manager {
	a.update.once.Do(func() {
		exe, _ := os.Executable()
		install := filepath.Dir(exe)
		cache, _ := updater.CacheDir(install)
		a.update.manager = updater.New(updater.Options{Install: install, Cache: cache, Version: a.cfg.Version, Client: newUpdateClient(), DirectClient: newUpdateDirectClient()})
	})
	return a.update.manager
}
func (a *App) GetUpdateState() updater.State   { return a.updates().State() }
func (a *App) Version() string                 { return a.cfg.Version }
func (a *App) BeginUpdateCheck() updater.State { return a.updates().Check() }
func (a *App) DownloadUpdate() error           { return a.updates().Download() }
func (a *App) CancelUpdate()                   { a.updates().Cancel() }
func (a *App) DismissUpdateResult()            { a.updates().DismissResult() }

type InstallReply struct {
	NeedsConfirm  bool `json:"needsConfirm"`
	ActiveWindows int  `json:"activeWindows"`
}

func (a *App) InstallUpdate(confirmed bool, quit func()) (InstallReply, error) {
	a.update.mu.Lock()
	defer a.update.mu.Unlock()
	if a.update.installing {
		return InstallReply{}, fmt.Errorf("更新正在安装")
	}
	a.mu.RLock()
	count := 0
	for _, s := range a.sessions {
		if s.runner != nil {
			count++
		}
	}
	for _, s := range a.appWins {
		if s.runner != nil {
			count++
		}
	}
	a.mu.RUnlock()
	if count > 0 && !confirmed {
		return InstallReply{NeedsConfirm: true, ActiveWindows: count}, nil
	}
	if !a.updates().State().CanInstall {
		return InstallReply{}, fmt.Errorf("更新尚未准备好")
	}
	a.update.installing = true
	go func() {
		p, err := a.updates().PrepareInstall()
		if err == nil {
			err = updater.Launch(p)
		}
		if err != nil {
			a.updates().InstallError(err)
			a.update.mu.Lock()
			a.update.installing = false
			a.update.mu.Unlock()
			return
		}
		quit()
	}()
	return InstallReply{}, nil
}

// 整个 Start 持锁：确认安装后不会漏掉仍在连接中的新会话。
func (a *App) guardUpdateStart() (func(), error) {
	a.update.mu.RLock()
	if a.update.installing {
		a.update.mu.RUnlock()
		return nil, fmt.Errorf("正在重启更新，请稍后再投屏")
	}
	return a.update.mu.RUnlock, nil
}
