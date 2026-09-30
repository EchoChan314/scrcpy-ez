package app

import (
	"net/http"
	"net/url"
	"scrcpy-ez/gui/internal/updater"
	"time"
)

type UpdateInfo = updater.ReleaseInfo

// 兼容旧绑定；正式界面使用异步 BeginUpdateCheck 和 GetUpdateState。
func (a *App) CheckUpdate() UpdateInfo { return a.updates().Check().Info }
func versionLess(a, b string) bool     { return updater.VersionLess(a, b) }
func parseVerTri(s string) []int       { return updater.ParseVersion(s) }

func newUpdateClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 8 * time.Second
	if p := systemProxyURL(); p != "" {
		if u, err := url.Parse("http://" + p); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	} else {
		tr.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{Timeout: 8 * time.Second, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// A separate direct path prevents an unavailable system proxy from blocking both sources.
func newUpdateDirectClient() *http.Client {
	req, _ := http.NewRequest(http.MethodGet, updater.LatestURL, nil)
	envProxy, _ := http.ProxyFromEnvironment(req)
	if systemProxyURL() == "" && envProxy == nil {
		return nil
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.ResponseHeaderTimeout = 8 * time.Second
	return &http.Client{Timeout: 8 * time.Second, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
