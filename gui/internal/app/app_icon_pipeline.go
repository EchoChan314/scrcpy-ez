package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var errIncompleteAppIcons = errors.New("incomplete app icons")
var appReadSlots = make(chan struct{}, 2)

func takeAppReadSlot(ctx context.Context) (func(), error) {
	select {
	case appReadSlots <- struct{}{}:
		return func() { <-appReadSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func appIconChunkSize(perItem time.Duration, wireless bool) int {
	if perItem <= 0 {
		return 24
	}
	if perItem > 400*time.Millisecond {
		return 16
	}
	if wireless {
		return 48
	}
	return 64
}

func (a *App) runAppIconDeltaForJob(identity, serial string, helper appServerHelper, items []AppListItem, wanted, removed []string, job *appReadJob) error {
	if !a.appReadValid(identity, serial, job) {
		if job.ctx.Err() != nil {
			return job.ctx.Err()
		}
		return errors.New("device owner changed")
	}
	dir := a.iconsDirFor(identity)
	if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dir), ".export-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	a.updateAppReadState(identity, job, func(s *appReadState) { s.Phase = "icons"; s.IconTotal = len(wanted); s.IconDone = 0 })
	if len(removed) > 0 {
		if _, err = a.commitIconDelta(identity, serial, stage, items, nil, removed); err != nil {
			return err
		}
	}
	partial := false
	for offset := 0; offset < len(wanted); {
		serial = a.bestEnumSerial(identity, serial)
		a.selectAppReadSerial(identity, serial, job)
		if !a.appReadValid(identity, serial, job) {
			if job.ctx.Err() != nil {
				return job.ctx.Err()
			}
			return errors.New("device owner changed")
		}
		count := appIconChunkSize(job.performance.IconPerApp, appReadWireless(serial))
		chunk := iconExportChunks(wanted[offset:min(offset+count, len(wanted))])[0]
		batchStage, err := os.MkdirTemp(stage, "batch-")
		if err != nil {
			return err
		}
		changed, batchErr := a.readAppIconBatch(identity, serial, helper, items, chunk, batchStage, job)
		os.RemoveAll(batchStage)
		offset += len(chunk)
		if batchErr != nil || len(changed) < len(chunk) {
			partial = true
		}
		a.markIconsChanged(identity, changed)
		a.updateAppReadState(identity, job, func(s *appReadState) {
			s.IconDone += len(changed)
			if len(changed) > 0 {
				s.IconRevision++
			}
		})
		a.touchAppBusy(identity)
		if job.ctx.Err() != nil {
			return job.ctx.Err()
		}
		if batchErr != nil && !a.appReadDeviceOnline(identity) {
			return batchErr
		}
	}
	if partial {
		return errIncompleteAppIcons
	}
	return nil
}

func (a *App) appReadDeviceOnline(identity string) bool {
	if a.appReadOps.shell != nil {
		return true
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, d := range a.devices {
		if d.Identity == identity && d.State == "device" && !d.Connecting && !d.Pairing {
			return true
		}
	}
	return false
}

func (a *App) readAppIconBatch(identity, serial string, helper appServerHelper, items []AppListItem, wanted []string, stage string, job *appReadJob) ([]string, error) {
	release, err := takeAppReadSlot(job.ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	token := make([]byte, 12)
	if _, err = rand.Read(token); err != nil {
		return nil, err
	}
	remote := fmt.Sprintf("/data/local/tmp/scrcpy/icons-job-%x", token)
	defer a.cleanRemoteIconStage(serial, remote)
	wireless := appReadWireless(serial)
	d := newAppReadDeadline(job.ctx, appIconBudget(len(wanted), wireless, job.performance.IconPerApp), 2*time.Minute, wireless, job.performance.IconPerApp)
	start := time.Now()
	out, exportErr := a.appReadShell(d.ctx, serial, helper.command("export_app_icons="+strings.Join(wanted, ",")+" export_app_icons_dir="+remote), func(line string) {
		if p, ok := parseAppReadProgress(line); ok && p.Phase == "icons" {
			d.progress(p)
		}
	})
	if d.ctx.Err() != nil {
		exportErr = d.ctx.Err()
	}
	d.close()
	if job.ctx.Err() != nil {
		return nil, job.ctx.Err()
	}
	if !a.appReadValid(identity, serial, job) || !a.iconExportOwnerMatches(identity, out) {
		return nil, errors.New("icon owner changed or unavailable")
	}
	if exportErr == nil && !strings.Contains(out, "Exported icons:") {
		exportErr = errIncompleteAppIcons
	}
	if exportErr == nil {
		job.performance.IconPerApp = observeAppReadDuration(job.performance.IconPerApp, time.Since(start)/time.Duration(max(len(wanted), 1)))
	}
	// Each private directory holds only one bounded batch. Even a partial pull
	// may contribute validated PNGs; unsuccessful files keep their old stamps.
	ctx, cancel := context.WithTimeout(job.ctx, appTransferBudget(int64(len(wanted))*32<<10, wireless, job.performance.PullBytesPerSecond))
	start = time.Now()
	pullErr := a.appReadPull(ctx, serial, remote+"/.", stage)
	cancel()
	if !a.appReadValid(identity, serial, job) {
		return nil, errors.New("device owner changed")
	}
	if pullErr == nil {
		var bytes int64
		entries, _ := os.ReadDir(stage)
		for _, entry := range entries {
			if !entry.IsDir() {
				if info, e := entry.Info(); e == nil {
					bytes += info.Size()
				}
			}
		}
		if bytes > 0 && time.Since(start) > 0 {
			sample := float64(bytes) / time.Since(start).Seconds()
			old := job.performance.PullBytesPerSecond
			if old > 0 {
				sample = old*.75 + sample*.25
			}
			job.performance.PullBytesPerSecond = sample
		}
	}
	changed, commitErr := a.commitIconDelta(identity, serial, stage, items, wanted, nil)
	if commitErr != nil {
		return changed, commitErr
	}
	if pullErr != nil {
		return changed, pullErr
	}
	return changed, exportErr
}
