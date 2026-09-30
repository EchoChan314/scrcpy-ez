package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type provider struct{ id, name, repo, api string }

var providers = []provider{
	{"github", "GitHub", RepoURL, "https://api.github.com/repos/kinewe/scrcpy-ez/releases/latest"},
	{"gitee", "Gitee", GiteeRepoURL, "https://gitee.com/api/v5/repos/kinewe/scrcpy-ez/releases/latest"},
}

func sameVersion(a, b string) bool {
	return ParseVersion(a) != nil && ParseVersion(b) != nil && !VersionLess(a, b) && !VersionLess(b, a)
}

func validSource(s DownloadSource, tag string) bool {
	if !AllowedURL(s.URL) || ParseVersion(tag) == nil || s.Size < 0 || s.Size > MaxDownload || (s.Digest != "" && !sha256Digest.MatchString(s.Digest)) {
		return false
	}
	for _, p := range providers {
		if s.ID != p.id {
			continue
		}
		prefix := p.repo + "/releases/download/"
		if !strings.HasPrefix(s.URL, prefix) {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(s.URL, prefix), "/")
		if len(parts) != 2 || !sameVersion(parts[0], tag) {
			continue
		}
		for _, name := range []string{"scrcpy-ez-" + strings.TrimPrefix(parts[0], "v") + ".zip", "scrcpy-ez-v" + strings.TrimPrefix(parts[0], "v") + ".zip"} {
			if parts[1] == name {
				return true
			}
		}
	}
	return false
}

func pageForSource(s DownloadSource) string {
	for _, p := range providers {
		prefix := p.repo + "/releases/download/"
		if s.ID == p.id && strings.HasPrefix(s.URL, prefix) {
			parts := strings.Split(strings.TrimPrefix(s.URL, prefix), "/")
			if len(parts) == 2 && ParseVersion(parts[0]) != nil {
				return p.repo + "/releases/tag/" + parts[0]
			}
		}
	}
	return GiteeLatestURL
}

func fixedSources(p provider, tag string) []DownloadSource {
	var out []DownloadSource
	for _, name := range []string{"scrcpy-ez-" + strings.TrimPrefix(tag, "v") + ".zip", "scrcpy-ez-v" + strings.TrimPrefix(tag, "v") + ".zip"} {
		out = append(out, DownloadSource{ID: p.id, Name: p.name, URL: p.repo + "/releases/download/" + tag + "/" + name})
	}
	return out
}

type releaseResult struct {
	info ReleaseInfo
	err  error
}

func FetchWithClients(ctx context.Context, client, direct *http.Client, current string) (ReleaseInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	ch := make(chan releaseResult, len(providers))
	for _, p := range providers {
		go func(p provider) {
			i, e := fetchProviderRoutes(ctx, p, client, direct, current)
			ch <- releaseResult{i, e}
		}(p)
	}
	info := ReleaseInfo{Current: current, RepoURL: RepoURL, DownloadURL: LatestURL, PageURL: LatestURL}
	var found []releaseResult
	for range providers {
		select {
		case r := <-ch:
			found = append(found, r)
		case <-ctx.Done():
			for {
				select {
				case r := <-ch:
					found = append(found, r)
				default:
					goto collected
				}
			}
		}
	}
collected:
	for _, r := range found {
		if ParseVersion(r.info.Latest) != nil && (info.Latest == "" || VersionLess(info.Latest, r.info.Latest)) {
			info.Latest = r.info.Latest
			info.RepoURL, info.PageURL = r.info.RepoURL, r.info.PageURL
		}
	}
	if info.Latest == "" {
		return info, fmt.Errorf("GitHub 和 Gitee 均无法检查更新，请检查网络后重试")
	}
	info.HasNew = VersionLess(current, info.Latest)
	validProviders := 0
	lagging := false
	for _, p := range providers {
		for _, r := range found {
			if r.info.RepoURL != p.repo || ParseVersion(r.info.Latest) == nil {
				continue
			}
			validProviders++
			if !sameVersion(r.info.Latest, info.Latest) {
				lagging = true
				continue
			}
			for _, s := range r.info.Sources {
				if validSource(s, r.info.Latest) {
					if info.Digest == "" && s.Digest != "" {
						info.Digest = s.Digest
					}
					if info.Size == 0 && s.Size > 0 {
						info.Size = s.Size
					}
					info.Sources = append(info.Sources, s)
				}
			}
		}
	}
	filtered := info.Sources[:0]
	for _, s := range info.Sources {
		if (info.Size > 0 && s.Size > 0 && info.Size != s.Size) || (info.Digest != "" && s.Digest != "" && !strings.EqualFold(info.Digest, s.Digest)) {
			continue
		}
		s.Size, s.Digest = info.Size, info.Digest
		filtered = append(filtered, s)
	}
	info.Sources = filtered
	if validProviders < len(providers) {
		info.Notice = "部分通道暂不可用，已使用可访问通道的版本信息。"
	}
	if lagging {
		info.Notice = "两个通道尚未同步，仅使用最高稳定版本。"
	}
	if len(info.Sources) > 0 {
		info.DownloadURL = info.Sources[0].URL
		info.PageURL = pageForSource(info.Sources[0])
		if info.Sources[0].ID == "gitee" {
			info.RepoURL = GiteeRepoURL
		}
	} else if info.HasNew {
		info.PackageMissing = true
		return info, fmt.Errorf("发行版本 %s 尚未提供可用的 Windows 更新包", info.Latest)
	}
	return info, nil
}

func fetchProviderRoutes(ctx context.Context, p provider, configured, direct *http.Client, current string) (ReleaseInfo, error) {
	if configured == nil {
		configured = &http.Client{Timeout: 6 * time.Second}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type route struct {
		client *http.Client
		name   string
	}
	routes := []route{{configured, "configured"}}
	if direct != nil && direct != configured {
		if p.id == "gitee" {
			routes = []route{{direct, "direct"}, {configured, "configured"}}
		} else {
			routes = append(routes, route{direct, "direct"})
		}
	}
	ch := make(chan releaseResult, len(routes))
	launch := func(r route) {
		go func() {
			metadataClient := *r.client
			metadataClient.Timeout = 6 * time.Second
			metadataClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			var i ReleaseInfo
			var e error
			if p.id == "github" {
				i, e = fetchGitHub(ctx, &metadataClient, current)
				s := DownloadSource{ID: p.id, Name: p.name, URL: i.DownloadURL, Size: i.Size, Digest: i.Digest, Route: r.name}
				if len(i.Sources) == 0 && validSource(s, i.Latest) {
					i.Sources = []DownloadSource{s}
				}
			} else {
				i, e = fetchGitee(ctx, &metadataClient, current)
			}
			for n := range i.Sources {
				i.Sources[n].Route = r.name
			}
			i.RepoURL = p.repo
			ch <- releaseResult{i, e}
		}()
	}
	launch(routes[0])
	launched, completed := 1, 0
	timer := time.NewTimer(750 * time.Millisecond)
	defer timer.Stop()
	var last releaseResult
	for completed < len(routes) {
		select {
		case r := <-ch:
			completed++
			last = r
			if ParseVersion(r.info.Latest) != nil {
				return r.info, r.err
			}
			if launched < len(routes) {
				launch(routes[launched])
				launched++
			}
		case <-timer.C:
			if launched < len(routes) {
				launch(routes[launched])
				launched++
			}
		case <-ctx.Done():
			return last.info, fmt.Errorf("%s 版本检查超时", p.name)
		}
	}
	return last.info, last.err
}

func fetchGitee(ctx context.Context, client *http.Client, current string) (ReleaseInfo, error) {
	info := ReleaseInfo{Current: current, RepoURL: GiteeRepoURL, PageURL: GiteeLatestURL, DownloadURL: GiteeLatestURL}
	resp, err := do(ctx, client, http.MethodGet, providers[1].api)
	if err == nil {
		var release struct {
			Tag        string `json:"tag_name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
			Assets     []struct {
				Name   string `json:"name"`
				URL    string `json:"browser_download_url"`
				Size   int64  `json:"size"`
				Digest string `json:"digest"`
			} `json:"assets"`
		}
		if resp.StatusCode == http.StatusOK {
			err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&release)
		} else {
			err = fmt.Errorf("版本服务返回 %d", resp.StatusCode)
		}
		resp.Body.Close()
		if err == nil && (release.Draft || release.Prerelease) {
			return info, fmt.Errorf("Gitee 尚未提供稳定发行版本")
		}
		if err == nil && !release.Draft && !release.Prerelease && ParseVersion(release.Tag) != nil {
			info.Latest, info.HasNew = release.Tag, VersionLess(current, release.Tag)
			info.PageURL = GiteeRepoURL + "/releases/tag/" + release.Tag
			for _, a := range release.Assets {
				s := DownloadSource{ID: "gitee", Name: "Gitee", URL: a.URL, Size: a.Size, Digest: a.Digest}
				if validSource(s, release.Tag) && (a.Name == "scrcpy-ez-"+strings.TrimPrefix(release.Tag, "v")+".zip" || a.Name == "scrcpy-ez-v"+strings.TrimPrefix(release.Tag, "v")+".zip") {
					info.Sources = []DownloadSource{s}
					info.DownloadURL, info.Size, info.Digest = s.URL, s.Size, s.Digest
					return info, nil
				}
			}
			return info, fmt.Errorf("发行版本尚未提供可用的 Windows 更新包")
		}
	}
	fallback := *client
	fallback.Timeout = 3 * time.Second
	resp, err = do(ctx, &fallback, http.MethodHead, GiteeLatestURL)
	if err != nil {
		return info, fmt.Errorf("Gitee 无法检查更新")
	}
	loc, status := resp.Header.Get("Location"), resp.StatusCode
	resp.Body.Close()
	base, _ := url.Parse(GiteeLatestURL)
	target, e := base.Parse(loc)
	if e != nil || loc == "" || status < 300 || status >= 400 || target.Scheme != "https" || target.Host != base.Host || !strings.HasPrefix(target.Path, "/kinewe/scrcpy-ez/releases/tag/") || target.RawQuery != "" {
		return info, fmt.Errorf("Gitee 无法获取最新发行版本")
	}
	tag := strings.TrimPrefix(target.Path, "/kinewe/scrcpy-ez/releases/tag/")
	if ParseVersion(tag) == nil {
		return info, fmt.Errorf("发行版本号无效")
	}
	info.Latest, info.HasNew, info.PageURL = tag, VersionLess(current, tag), GiteeRepoURL+"/releases/tag/"+tag
	info.Sources = fixedSources(providers[1], tag)
	info.DownloadURL = info.Sources[0].URL
	return info, nil
}
