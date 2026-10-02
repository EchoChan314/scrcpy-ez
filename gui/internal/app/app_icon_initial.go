package app

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

const initialAppIconWait = 8 * time.Second

type initialAppIconGate struct {
	deadline time.Time // zero means an existing cache: never mask ordinary background checks
	released bool
}

// Once per device/job lifecycle, outside a.mu and outside the 700ms snapshot path.
// index.json or an empty directory alone does not constitute an icon cache.
func (a *App) prepareInitialAppIconGate(identity string) {
	if identity == "" || strings.HasPrefix(identity, "pending:") {
		return
	}
	a.mu.RLock()
	_, checked := a.appIconInitial[identity]
	a.mu.RUnlock()
	if checked {
		return
	}
	hasIcons := hasAppIconFiles(a.iconsDirFor(identity))
	if !hasIcons {
		if legacy := a.legacyIconsDirFor(identity); legacy != "" {
			hasIcons = hasAppIconFiles(legacy)
		}
	}
	gate := initialAppIconGate{}
	if !hasIcons {
		gate.deadline = time.Now().Add(initialAppIconWait)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.appIconInitial == nil {
		a.appIconInitial = map[string]initialAppIconGate{}
	}
	if _, exists := a.appIconInitial[identity]; !exists {
		a.appIconInitial[identity] = gate
	}
}

func hasAppIconFiles(dir string) bool {
	files, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, file := range files {
		ext := filepath.Ext(file.Name())
		pkg := strings.TrimSuffix(file.Name(), ext)
		if file.IsDir() || !strings.EqualFold(ext, ".png") || !rePkgName.MatchString(pkg) {
			continue
		}
		if info, err := file.Info(); err == nil && info.Size() > 0 {
			return true
		}
	}
	return false
}

// Caller holds a.mu. This is only a map/time check; no disk or ADB access.
func (a *App) initialAppIconBusyLocked(identity string, now time.Time) bool {
	gate, ok := a.appIconInitial[identity]
	_, running := a.appListBusy[identity]
	return ok && running && !gate.released && now.Before(gate.deadline)
}

func (a *App) releaseInitialAppIconGateLocked(identity string) {
	if gate, ok := a.appIconInitial[identity]; ok {
		gate.released = true
		a.appIconInitial[identity] = gate
	}
}
