package updater

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type localDownloadError struct{ error }

func (e localDownloadError) Unwrap() error { return e.error }

func verifyArchive(path string, info ReleaseInfo) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Size() == 0 || st.Size() > MaxDownload {
		return fmt.Errorf("更新包大小异常")
	}
	hash, size, err := fileHash(path)
	if err != nil {
		return err
	}
	if size == 0 || size > MaxDownload || (info.Size > 0 && size != info.Size) || (info.Digest != "" && !strings.EqualFold(info.Digest, "sha256:"+hash)) {
		return fmt.Errorf("更新包完整性校验失败")
	}
	return nil
}

func (m *Manager) downloadArchive(ctx context.Context, c *cached) error {
	list, err := m.selectSources(ctx, c.Info)
	if err != nil {
		return err
	}
	archive := filepath.Join(c.Plan.Work, "package.zip")
	var lastErr error
	preferred := -1
	// Visit other paths before retrying a failed path. Bounded retries avoid endless switching.
	for attempt := 0; attempt < 8; attempt++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		chosen := -1
		for n := range list {
			if !list[n].blocked && list[n].attempts < 2 && (chosen < 0 || list[n].attempts < list[chosen].attempts) {
				chosen = n
			}
		}
		if preferred >= 0 && !list[preferred].blocked && list[preferred].attempts < 2 {
			chosen = preferred
		}
		preferred = -1
		if chosen < 0 {
			break
		}
		cand := &list[chosen]
		if cand.attempts > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
		cand.attempts++
		if c.Info.Size > 0 && cand.source.Size > 0 && c.Info.Size != cand.source.Size {
			cand.blocked = true
			lastErr = fmt.Errorf("下载包大小与发行信息不一致")
			continue
		}
		if c.Info.Size == 0 && cand.source.Size > 0 {
			c.Info.Size = cand.source.Size
		}
		c.Info.DownloadURL = cand.source.URL
		c.Info.PageURL = pageForSource(cand.source)
		if err = WriteJSON(filepath.Join(m.opts.Cache, "candidate.json"), c); err != nil {
			return err
		}
		m.change(func(s *State) {
			s.Phase, s.Source = "downloading", cand.source.Name
			s.Info = c.Info
			s.Downloaded, s.Total, s.Speed = 0, c.Info.Size, 0
			if attempt == 0 {
				s.Message = "正在下载，关闭弹窗后仍会继续"
			} else {
				s.Message = "已切换下载连接，正在继续下载…"
			}
		})
		alternate := fallbackMeasure{}
		type alternative struct {
			index     int
			candidate downloadCandidate
		}
		var alternatives []alternative
		for n, other := range list {
			if n != chosen && !other.blocked && other.attempts == 0 {
				alternatives = append(alternatives, alternative{n, other})
			}
			if n != chosen && !other.blocked && other.attempts == 0 && other.rate > alternate.rate {
				alternate = fallbackMeasure{other.rate, other.source.URL, other.ranged}
			}
		}
		recommended := make(chan int, 1)
		var reconsider func(context.Context, transferStatus) bool
		downloadInfo := c.Info
		if len(alternatives) > 0 {
			reconsider = func(active context.Context, status transferStatus) bool {
				probeCtx, cancel := context.WithTimeout(active, 3*time.Second)
				defer cancel()
				results := make(chan alternative, len(alternatives))
				for _, alt := range alternatives {
					go func(alt alternative) { alt.candidate = m.probe(probeCtx, alt.candidate); results <- alt }(alt)
				}
				for range alternatives {
					select {
					case alt := <-results:
						canResume := resumeAlternative(downloadInfo, alt.candidate.source, alt.candidate.ranged, status.etag)
						if alt.candidate.available && switchUseful(status.rate, alt.candidate.rate, status.total, status.remaining, canResume) {
							recommended <- alt.index
							return true
						}
					case <-probeCtx.Done():
						return false
					}
				}
				return false
			}
		}
		err = m.transferFrom(ctx, c.Info, c.Plan.Work, cand.source.Route, alternate, reconsider)
		select {
		case preferred = <-recommended:
		default:
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var local localDownloadError
		if errors.As(err, &local) {
			return err
		}
		if err == nil {
			m.change(func(s *State) {
				s.Phase, s.Message, s.Speed = "validating", "下载完成，正在校验更新包…", 0
			})
			err = verifyArchive(archive, c.Info)
			if err == nil {
				return nil
			}
			// A complete bad package invalidates this URL, regardless of network route.
			for n := range list {
				if list[n].source.URL == cand.source.URL {
					list[n].blocked = true
				}
			}
			for _, name := range []string{"package.zip", "package.part", "partial.json"} {
				if e := os.Remove(filepath.Join(c.Plan.Work, name)); e != nil && !os.IsNotExist(e) {
					return e
				}
			}
		}
		lastErr = err
		cand.available, cand.rate = false, 0
		m.change(func(s *State) {
			s.Phase, s.Message, s.Speed = "selecting", "当前下载连接不可用，正在尝试其他来源…", 0
		})
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的更新下载连接")
	}
	return fmt.Errorf("下载未完成，已尝试可用来源，可稍后继续：%w", lastErr)
}
