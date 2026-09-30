package updater

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type downloadCandidate struct {
	source    DownloadSource
	rate      int64
	available bool
	blocked   bool
	attempts  int
	ranged    bool
}

func (m *Manager) routeClient(route string) *http.Client {
	if route == "direct" && m.opts.DirectClient != nil {
		return m.opts.DirectClient
	}
	return m.opts.Client
}

func (m *Manager) downloadClient(route string, timeout time.Duration) *http.Client {
	client := *m.routeClient(route)
	client.Timeout = timeout
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 8 || (!m.opts.AllowTestHTTP && !AllowedURL(req.URL.String())) {
			return fmt.Errorf("下载重定向不受信任")
		}
		return nil
	}
	return &client
}

func sourceList(info ReleaseInfo) []DownloadSource {
	if len(info.Sources) > 0 {
		return append([]DownloadSource(nil), info.Sources...)
	}
	s := DownloadSource{Name: "下载源", URL: info.DownloadURL, Size: info.Size, Digest: info.Digest}
	for _, p := range providers {
		if strings.HasPrefix(s.URL, p.repo+"/releases/download/") {
			s.ID, s.Name = p.id, p.name
		}
	}
	return []DownloadSource{s}
}

func (m *Manager) candidates(info ReleaseInfo) []downloadCandidate {
	var out []downloadCandidate
	seen := make(map[string]bool)
	for _, s := range sourceList(info) {
		if !m.opts.AllowTestHTTP && !validSource(s, info.Latest) {
			continue
		}
		s.Size, s.Digest = info.Size, info.Digest
		routes := []string{"configured"}
		if m.opts.DirectClient != nil && m.opts.DirectClient != m.opts.Client {
			if s.ID == "gitee" || s.Route == "direct" {
				routes = []string{"direct", "configured"}
			} else {
				routes = append(routes, "direct")
			}
		}
		for _, r := range routes {
			key := s.URL + "|" + r
			if !seen[key] {
				seen[key] = true
				s.Route = r
				out = append(out, downloadCandidate{source: s})
			}
		}
	}
	return out
}

// Real bytes, including connect/redirect time, rather than HEAD latency alone.
func (m *Manager) probe(ctx context.Context, c downloadCandidate) downloadCandidate {
	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.source.URL, nil)
	if err != nil {
		return c
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Range", "bytes=0-524287")
	resp, err := m.downloadClient(c.source.Route, 5*time.Second).Do(req)
	if err != nil {
		return c
	}
	defer resp.Body.Close()
	total := resp.ContentLength
	if resp.StatusCode == http.StatusPartialContent {
		var start, end, n int64
		if _, err = fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &n); err != nil || start != 0 || end < 0 || n <= end || end >= 524288 {
			return c
		}
		total = n
		c.ranged = true
	} else if resp.StatusCode != http.StatusOK {
		return c
	}
	if total > MaxDownload || (c.source.Size > 0 && total > 0 && c.source.Size != total) {
		c.blocked = true
		return c
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	minimum := int64(32 << 10)
	if total > 0 && total < minimum {
		minimum = total
	}
	if int64(len(data)) < minimum || len(data) < 4 || string(data[:4]) != "PK\x03\x04" {
		return c
	}
	if total > 0 {
		c.source.Size = total
	}
	c.available = true
	c.rate = int64(float64(len(data)) / time.Since(started).Seconds())
	return c
}

func (m *Manager) selectSources(ctx context.Context, info ReleaseInfo) ([]downloadCandidate, error) {
	parent := ctx
	list := m.candidates(info)
	if len(list) == 0 {
		return nil, fmt.Errorf("发行版本没有可用下载源")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	type result struct {
		index     int
		candidate downloadCandidate
	}
	ch := make(chan result, len(list))
	for n, c := range list {
		go func(n int, c downloadCandidate) { ch <- result{n, m.probe(ctx, c)} }(n, c)
	}
	var grace <-chan time.Time
	var graceTimer *time.Timer
	defer func() {
		if graceTimer != nil {
			graceTimer.Stop()
		}
	}()
	for remaining := len(list); remaining > 0; remaining-- {
		select {
		case r := <-ch:
			list[r.index] = r.candidate
			if r.candidate.available && grace == nil {
				graceTimer = time.NewTimer(time.Second)
				grace = graceTimer.C
			}
		case <-grace:
			cancel()
			goto selected
		case <-ctx.Done():
			goto selected
		}
	}
selected:
	// Keep unmeasured paths for failover, without racing their writes into this slice.
	for {
		select {
		case r := <-ch:
			list[r.index] = r.candidate
		default:
			goto drained
		}
	}
drained:
	if parent.Err() != nil {
		return nil, parent.Err()
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].blocked != list[j].blocked {
			return !list[i].blocked
		}
		if list[i].available != list[j].available {
			return list[i].available
		}
		return list[i].rate > list[j].rate
	})
	return list, nil
}

func packageIdentity(a, b ReleaseInfo) bool {
	if !sameVersion(a.Latest, b.Latest) {
		return false
	}
	if a.Digest != "" || b.Digest != "" {
		return a.Digest != "" && strings.EqualFold(a.Digest, b.Digest) && (a.Size == 0 || b.Size == 0 || a.Size == b.Size)
	}
	for _, x := range sourceList(a) {
		for _, y := range sourceList(b) {
			if x.URL == y.URL {
				return true
			}
		}
	}
	return false
}
