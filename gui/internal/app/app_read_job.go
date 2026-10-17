package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"scrcpy-ez/gui/internal/adbparse"
)

type appReadJob struct {
	ctx         context.Context
	cancel      context.CancelFunc
	id          uint64
	serial      string
	performance appReadPerformance
}

type appReadState struct {
	Job          uint64 `json:"job"`
	Phase        string `json:"phase,omitempty"`
	Current      int    `json:"current,omitempty"`
	Total        int    `json:"total,omitempty"`
	AppCount     int    `json:"appCount,omitempty"`
	IconDone     int    `json:"iconDone,omitempty"`
	IconTotal    int    `json:"iconTotal,omitempty"`
	Revision     uint64 `json:"revision,omitempty"`
	IconRevision uint64 `json:"iconRevision,omitempty"`
	HasCatalog   bool   `json:"hasCatalog,omitempty"`
	Error        string `json:"error,omitempty"`
	Partial      bool   `json:"partial,omitempty"`
}

// Optional deterministic I/O hooks are used by pipeline tests. Production
// always calls the bounded ADB primitives and content-addressed helper.
type appReadOperations struct {
	helper func() (appServerHelper, error)
	shell  func(context.Context, string, string, func(string)) (string, error)
	push   func(context.Context, string, string, string) error
	pull   func(context.Context, string, string, string) error
}

func (a *App) startAppReadJob(identity, serial string) *appReadJob {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.appReadClosing {
		delete(a.appListBusy, identity)
		delete(a.appListSilent, identity)
		a.releaseInitialAppIconGateLocked(identity)
		return nil
	}
	if a.appReadJobs == nil {
		a.appReadJobs = map[string]*appReadJob{}
	}
	if a.appReadStates == nil {
		a.appReadStates = map[string]appReadState{}
	}
	if a.appReadHistory == nil {
		a.appReadHistory = map[string]appReadPerformance{}
	}
	if a.appReadJobs[identity] != nil {
		return nil
	}
	if a.appListBusy == nil {
		a.appListBusy = map[string]time.Time{}
	}
	a.appListBusy[identity] = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), appReadHardLimit)
	a.appReadSequence++
	j := &appReadJob{ctx: ctx, cancel: cancel, id: a.appReadSequence, serial: serial, performance: a.appReadHistory[appReadHistoryKey(identity, serial)]}
	a.appReadJobs[identity] = j
	old := a.appReadStates[identity]
	a.appReadStates[identity] = appReadState{Job: j.id, Phase: "catalog", Revision: old.Revision, IconRevision: old.IconRevision, HasCatalog: old.HasCatalog, AppCount: old.AppCount}
	return j
}

func appReadWireless(serial string) bool {
	return strings.Contains(serial, ":") || adbparse.IsServiceTransport(serial)
}

func appReadHistoryKey(identity, serial string) string {
	if appReadWireless(serial) {
		return identity + "|wifi"
	}
	return identity + "|usb"
}

func (a *App) selectAppReadSerial(identity, serial string, job *appReadJob) {
	if appReadWireless(job.serial) != appReadWireless(serial) {
		a.mu.Lock()
		a.appReadHistory[appReadHistoryKey(identity, job.serial)] = job.performance
		job.performance = a.appReadHistory[appReadHistoryKey(identity, serial)]
		a.mu.Unlock()
	}
	job.serial = serial
}

func (a *App) appReadValid(identity, serial string, job *appReadJob) bool {
	if job.ctx.Err() != nil || a.appListKeyFor(serial) != identity {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.appReadJobs[identity] == job
}

func (a *App) updateAppReadState(identity string, job *appReadJob, update func(*appReadState)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.appReadJobs[identity] != job {
		return
	}
	s := a.appReadStates[identity]
	update(&s)
	a.appReadStates[identity] = s
}

func (a *App) finishAppReadJob(identity string, job *appReadJob, err error) {
	job.cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.appReadJobs[identity] != job {
		return
	}
	s := a.appReadStates[identity]
	if err != nil {
		if s.HasCatalog {
			s.Phase, s.Partial = "partial", true
		} else {
			s.Phase = "error"
		}
		s.Error = appReadErrorMessage(err)
	} else if s.Partial {
		s.Phase = "partial"
	} else {
		s.Phase = "complete"
	}
	a.appReadStates[identity] = s
	a.appReadHistory[appReadHistoryKey(identity, job.serial)] = job.performance
	delete(a.appReadJobs, identity)
	delete(a.appListBusy, identity)
	delete(a.appListSilent, identity)
	a.releaseInitialAppIconGateLocked(identity)
}

func appReadErrorMessage(err error) string {
	if errors.Is(err, errIncompleteAppIcons) {
		return "部分图标未完成，应用仍可使用；可稍后重试"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "读取超时或连接中断，可重试；已有列表和图标已保留"
	}
	text := err.Error()
	if strings.Contains(text, "catalog too large") || strings.Contains(text, "too many apps") || strings.Contains(text, "资源上限") {
		return "应用列表超过资源上限，已有列表已保留"
	}
	if strings.Contains(text, "owner") {
		return "设备连接已变化，请重新打开应用列表"
	}
	if strings.Contains(text, "physical identity") {
		return "设备身份校验未通过，请重新连接设备"
	}
	return "暂时未能完成读取，请确认设备已连接后重试"
}

func (a *App) appReadShell(ctx context.Context, serial, command string, onLine func(string)) (string, error) {
	if a.appReadOps.shell != nil {
		return a.appReadOps.shell(ctx, serial, command, onLine)
	}
	return a.adb.ShellOutStream(ctx, serial, command, appReadOutputLimit, onLine)
}

func (a *App) appReadPush(ctx context.Context, serial, local, remote string) error {
	if a.appReadOps.push != nil {
		return a.appReadOps.push(ctx, serial, local, remote)
	}
	return a.adb.PushFile(ctx, serial, local, remote)
}

func (a *App) appReadPull(ctx context.Context, serial, remote, local string) error {
	if a.appReadOps.pull != nil {
		return a.appReadOps.pull(ctx, serial, remote, local)
	}
	return a.adb.PullDir(ctx, serial, remote, local)
}

func observeAppReadDuration(old, sample time.Duration) time.Duration {
	if sample <= 0 {
		return old
	}
	if old <= 0 {
		return sample
	}
	return old*3/4 + sample/4
}
