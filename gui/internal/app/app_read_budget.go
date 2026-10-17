package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

const appProgressMarker = "SCEZ_APP_PROGRESS:"
const appReadOutputLimit = 12 << 20
const appCatalogBytesLimit = 8 << 20
const appReadHardLimit = 15 * time.Minute

type appReadProgress struct {
	Phase   string `json:"phase"`
	Current int    `json:"current"`
	Total   int    `json:"total"`
}

type appReadPerformance struct {
	CatalogPerApp, IconPerApp time.Duration
	PullBytesPerSecond        float64
}

func boundedDuration(value, low, high time.Duration) time.Duration {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func appCatalogBudget(count int, wireless bool, observed time.Duration) time.Duration {
	base, unit, minimum, maximum := 12*time.Second, 45*time.Millisecond, 20*time.Second, 4*time.Minute
	if wireless {
		base, unit, minimum, maximum = 18*time.Second, 60*time.Millisecond, 30*time.Second, 5*time.Minute
	}
	if observed > 0 {
		unit = boundedDuration(observed*2, unit, time.Second)
	}
	if count <= 0 {
		count = 400
	}
	return boundedDuration(base+time.Duration(count)*unit, minimum, maximum)
}

func appIconBudget(count int, wireless bool, observed time.Duration) time.Duration {
	base, unit := 8*time.Second, 250*time.Millisecond
	if wireless {
		base, unit = 12*time.Second, 350*time.Millisecond
	}
	if observed > 0 {
		unit = boundedDuration(observed*3, 80*time.Millisecond, 2*time.Second)
	}
	return boundedDuration(base+time.Duration(count)*unit, 12*time.Second, 2*time.Minute)
}

func appTransferBudget(bytes int64, wireless bool, bytesPerSecond float64) time.Duration {
	base, rate := 6*time.Second, float64(1<<20)
	if wireless {
		base, rate = 10*time.Second, 256<<10
	}
	if bytesPerSecond > 0 && bytesPerSecond < rate {
		rate = bytesPerSecond
	}
	rate = max(rate, 16<<10)
	return boundedDuration(base+time.Duration(float64(bytes)/rate*3*float64(time.Second)), 10*time.Second, 2*time.Minute)
}

// Progress can extend a small initial prediction for a large/slow device, but
// repeated heartbeats cannot keep a stalled command alive indefinitely.
type appReadDeadline struct {
	mu                   sync.Mutex
	ctx                  context.Context
	cancel               context.CancelFunc
	timer                *time.Timer
	start                time.Time
	hard, idle, observed time.Duration
	wireless             bool
	last, total          int
	phase                string
	closed               bool
}

func newAppReadDeadline(parent context.Context, initial, hard time.Duration, wireless bool, observed time.Duration) *appReadDeadline {
	ctx, cancel := context.WithCancel(parent)
	d := &appReadDeadline{ctx: ctx, cancel: cancel, start: time.Now(), hard: hard, wireless: wireless, observed: observed, last: -1}
	d.idle = 20 * time.Second
	if wireless {
		d.idle = 30 * time.Second
	}
	d.timer = time.AfterFunc(min(initial, hard), cancel)
	return d
}

func (d *appReadDeadline) progress(p appReadProgress) {
	if p.Total < 0 || p.Total > 100000 || p.Current < 0 || p.Current > p.Total {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.ctx.Err() != nil {
		return
	}
	if p.Phase == d.phase && p.Current <= d.last && p.Total <= d.total {
		return
	}
	// Only the first phase change is relevant within each stage command.
	if d.phase != "" && p.Phase != d.phase {
		return
	}
	d.phase, d.last, d.total = p.Phase, p.Current, p.Total
	elapsed := time.Since(d.start)
	remaining := appCatalogBudget(p.Total, d.wireless, d.observed) - elapsed
	idle := d.idle
	if p.Current == 0 {
		idle = boundedDuration(remaining/2, d.idle, 60*time.Second)
	}
	if p.Current > 0 {
		perItem := elapsed / time.Duration(p.Current)
		remaining = time.Duration(p.Total-p.Current)*max(perItem*2, d.observed*2) + 8*time.Second
		idle = boundedDuration(perItem*24, d.idle, 90*time.Second)
	}
	delay := min(d.hard-elapsed, min(max(remaining, d.idle), idle))
	if delay <= 0 {
		d.cancel()
		return
	}
	d.timer.Reset(delay)
}

func (d *appReadDeadline) close() {
	d.mu.Lock()
	d.closed = true
	d.timer.Stop()
	d.mu.Unlock()
	d.cancel()
}

func parseAppReadProgress(line string) (appReadProgress, bool) {
	i := strings.Index(line, appProgressMarker)
	if i < 0 || len(line)-i > 1024 {
		return appReadProgress{}, false
	}
	var p appReadProgress
	err := json.Unmarshal([]byte(line[i+len(appProgressMarker):]), &p)
	return p, err == nil && (p.Phase == "catalog" || p.Phase == "icons") && p.Total >= 0 && p.Total <= 100000 && p.Current >= 0 && p.Current <= p.Total
}
