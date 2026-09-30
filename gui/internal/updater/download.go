package updater

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

type partial struct {
	URL    string `json:"url"`
	ETag   string `json:"etag"`
	Digest string `json:"digest,omitempty"`
	Size   int64  `json:"size,omitempty"`
}

func (m *Manager) download(ctx context.Context, c *cached) error {
	for _, name := range []string{"download.lock", "package.zip", "package.part", "partial.json", "stage"} {
		if _, err := safePath(c.Plan.Work, name); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(c.Plan.Work, 0700); err != nil {
		return err
	}
	lock, err := lockFile(filepath.Join(c.Plan.Work, "download.lock"))
	if err != nil {
		return fmt.Errorf("此更新包正在被另一个 ez 下载：%w", err)
	}
	defer lock.Close()
	if err = WriteJSON(filepath.Join(m.opts.Cache, "candidate.json"), c); err != nil {
		return err
	}
	archive := filepath.Join(c.Plan.Work, "package.zip")
	if _, err = os.Stat(archive); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err = verifyArchive(archive, c.Info); err != nil {
		if removeErr := os.Remove(archive); removeErr != nil && !os.IsNotExist(removeErr) {
			return removeErr
		}
		if err = m.downloadArchive(ctx, c); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.change(func(s *State) { s.Phase, s.Message = "validating", "下载完成，正在校验并准备更新…" })
	if err = verifyArchive(archive, c.Info); err != nil {
		_ = os.Remove(archive)
		return err
	}
	// 只删除自己生成的 job/stage，不跟随符号链接。
	if st, e := os.Lstat(c.Plan.Stage()); e == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("更新暂存目录异常")
		}
		if e = os.RemoveAll(c.Plan.Stage()); e != nil {
			return e
		}
	}
	if err := os.MkdirAll(c.Plan.Stage(), 0700); err != nil {
		return err
	}
	files, err := Extract(ctx, archive, c.Plan.Stage())
	if err != nil {
		if ctx.Err() == nil {
			_ = os.Remove(archive)
		}
		return err
	}
	version, err := FileVersion(filepath.Join(c.Plan.Stage(), "scrcpy-ez.exe"))
	if err != nil {
		_ = os.Remove(archive)
		return err
	}
	if strings.TrimPrefix(version, "v") != strings.TrimPrefix(c.Info.Latest, "v") {
		_ = os.Remove(archive)
		return fmt.Errorf("更新包程序版本与发行版本不一致")
	}
	c.Plan.Files = files
	c.Ready = true
	if err := ctx.Err(); err != nil {
		return err
	}
	return WriteJSON(filepath.Join(m.opts.Cache, "candidate.json"), c)
}

func (m *Manager) transfer(ctx context.Context, info ReleaseInfo, work string) error {
	return m.transferFrom(ctx, info, work, "configured", fallbackMeasure{}, nil)
}

type fallbackMeasure struct {
	rate   int64
	url    string
	ranged bool
}
type transferStatus struct {
	rate, total, remaining int64
	etag                   string
}

func switchUseful(rate, alternative, total, remaining int64, resume bool) bool {
	if alternative <= rate*2 || alternative <= 0 || total <= 0 || remaining <= 5<<20 {
		return false
	}
	bytes := total
	if resume {
		bytes = remaining
	}
	if rate <= 0 {
		return true
	}
	// Include another connection and require a material time saving after any restart.
	return float64(bytes)/float64(alternative)+2 < float64(remaining)/float64(rate)*0.75
}

func resumeAlternative(info ReleaseInfo, source DownloadSource, ranged bool, etag string) bool {
	if !ranged {
		return false
	}
	return (sha256Digest.MatchString(info.Digest) && info.Size > 0) || (source.URL == info.DownloadURL && etag != "" && !strings.HasPrefix(etag, "W/"))
}

func (m *Manager) transferFrom(ctx context.Context, info ReleaseInfo, work, route string, alternate fallbackMeasure, reconsider func(context.Context, transferStatus) bool) error {
	client := m.downloadClient(route, 30*time.Minute)
	partPath := filepath.Join(work, "package.part")
	metaPath := filepath.Join(work, "partial.json")
	var meta partial
	_ = ReadJSON(metaPath, &meta)
	offset := int64(0)
	sameURL := meta.URL == info.DownloadURL
	strongETag := meta.ETag != "" && !strings.HasPrefix(meta.ETag, "W/")
	sharedDigest := info.Digest != "" && sha256Digest.MatchString(info.Digest) && strings.EqualFold(meta.Digest, info.Digest) && info.Size > 0 && meta.Size == info.Size
	compatibleMeta := (meta.Digest == "" || info.Digest == "" || strings.EqualFold(meta.Digest, info.Digest)) && (meta.Size == 0 || info.Size == 0 || meta.Size == info.Size)
	if (sameURL && strongETag && compatibleMeta) || sharedDigest {
		if st, err := os.Stat(partPath); err == nil {
			offset = st.Size()
		}
	}
	if offset > MaxDownload {
		return fmt.Errorf("缓存文件异常")
	}
	if info.Size > 0 && offset > info.Size {
		offset = 0
	}
	if offset > 0 && offset == info.Size && info.Digest != "" {
		if verifyArchive(partPath, info) == nil {
			return os.Rename(partPath, filepath.Join(work, "package.zip"))
		}
		offset = 0
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.DownloadURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		if sameURL && strongETag {
			req.Header.Set("If-Range", meta.ETag)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载连接失败：%w", err)
	}
	defer resp.Body.Close()
	total := info.Size
	if resp.StatusCode == http.StatusPartialContent {
		var start, end, n int64
		if _, e := fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &n); e != nil || start != offset || end < start || n != end+1 || (sameURL && strongETag && resp.Header.Get("ETag") != meta.ETag) {
			os.Remove(partPath)
			os.Remove(metaPath)
			return fmt.Errorf("续传信息发生变化，重新下载")
		}
		total = n
	} else if resp.StatusCode == http.StatusOK {
		offset = 0
		if resp.ContentLength > 0 {
			total = resp.ContentLength
		}
	} else {
		if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			os.Remove(partPath)
			os.Remove(metaPath)
		}
		return fmt.Errorf("下载服务返回 %d", resp.StatusCode)
	}
	if total > MaxDownload || (info.Size > 0 && total > 0 && total != info.Size) {
		return fmt.Errorf("下载包大小与发行信息不一致")
	}
	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_APPEND
	}
	out, err := os.OpenFile(partPath, flags, 0600)
	if err != nil {
		return localDownloadError{fmt.Errorf("写入更新缓存失败：%w", err)}
	}
	// Truncate first: a crash must never leave old bytes labelled as the new URL/ETag.
	if err = WriteJSON(metaPath, partial{URL: info.DownloadURL, ETag: resp.Header.Get("ETag"), Digest: info.Digest, Size: total}); err != nil {
		_ = out.Close()
		return localDownloadError{err}
	}
	m.change(func(s *State) { s.Downloaded, s.Total = offset, total })
	started := time.Now()
	downloaded := offset
	var last atomic.Int64
	var byteCount atomic.Int64
	byteCount.Store(offset)
	var stalled atomic.Int32
	last.Store(time.Now().UnixNano())
	idleDone := make(chan struct{})
	defer close(idleDone)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		windowStart, windowBytes := time.Now(), offset
		reconsidered := false
		for {
			select {
			case <-idleDone:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if time.Since(time.Unix(0, last.Load())) > 12*time.Second {
					stalled.Store(1)
					cancel()
					return
				}
				if elapsed := time.Since(windowStart); elapsed >= 10*time.Second {
					count := byteCount.Load()
					rate := int64(float64(count-windowBytes) / elapsed.Seconds())
					if time.Since(started) >= 20*time.Second && total-count > 5<<20 {
						etag := resp.Header.Get("ETag")
						canResume := resumeAlternative(info, DownloadSource{URL: alternate.url}, alternate.ranged, etag)
						improves := switchUseful(rate, alternate.rate, total, total-count, canResume)
						if !improves && rate < 256<<10 && !reconsidered && reconsider != nil {
							reconsidered = true
							improves = reconsider(ctx, transferStatus{rate, total, total - count, etag})
							// Do not switch after the active connection has recovered while probing.
							currentRate := int64(float64(byteCount.Load()-windowBytes) / time.Since(windowStart).Seconds())
							improves = improves && currentRate < 256<<10 && float64(currentRate) <= float64(rate)*1.25
							count = byteCount.Load()
						}
						if improves && total-count > 5<<20 && ctx.Err() == nil {
							stalled.Store(2)
							cancel()
							return
						}
					}
					windowStart, windowBytes = time.Now(), count
				}
			}
		}
	}()
	buf := make([]byte, 128<<10)
	var prefix [4]byte
	prefixLen := 0
	lastUI := time.Time{}
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if offset == 0 && prefixLen < len(prefix) {
				prefixLen += copy(prefix[prefixLen:], buf[:n])
				if prefixLen == len(prefix) && string(prefix[:]) != "PK\x03\x04" {
					err = fmt.Errorf("下载源没有返回有效的更新压缩包")
					break
				}
			}
			last.Store(time.Now().UnixNano())
			downloaded += int64(n)
			byteCount.Store(downloaded)
			if downloaded > MaxDownload {
				err = fmt.Errorf("更新包超过下载上限")
				break
			}
			if _, e := out.Write(buf[:n]); e != nil {
				err = localDownloadError{fmt.Errorf("更新缓存写入失败（可能磁盘空间不足）：%w", e)}
				break
			}
		}
		if time.Since(lastUI) > 200*time.Millisecond {
			seconds := time.Since(started).Seconds()
			speed := int64(float64(downloaded-offset) / seconds)
			m.change(func(s *State) { s.Downloaded, s.Total, s.Speed = downloaded, total, speed })
			lastUI = time.Now()
		}
		if readErr != nil {
			if readErr != io.EOF {
				err = fmt.Errorf("下载中断：%w", readErr)
			}
			break
		}
		if e := ctx.Err(); e != nil {
			err = e
			break
		}
	}
	if err == nil {
		if e := out.Sync(); e != nil {
			err = localDownloadError{e}
		}
	}
	closeErr := out.Close()
	if err == nil {
		if closeErr != nil {
			err = localDownloadError{closeErr}
		}
	}
	if stalled.Load() == 1 {
		return fmt.Errorf("下载源连续12秒未传输数据")
	}
	if stalled.Load() == 2 {
		return fmt.Errorf("下载源持续低速，尝试更快来源")
	}
	if err != nil {
		return err
	}
	if total > 0 && downloaded != total {
		return fmt.Errorf("更新包未下载完整")
	}
	m.change(func(s *State) { s.Downloaded, s.Total = downloaded, downloaded })
	if err = os.Rename(partPath, filepath.Join(work, "package.zip")); err != nil {
		return localDownloadError{err}
	}
	return nil
}
