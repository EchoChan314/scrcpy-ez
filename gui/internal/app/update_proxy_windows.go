//go:build windows

package app

// 系统代理读取（版本检查用）：Go 的 net/http 默认不读 Windows 系统代理（只读
// HTTP_PROXY/HTTPS_PROXY 环境变量），而国内 Clash 等代理软件默认开"系统代理"
// ——不显式尊重它会直连 GitHub 失败（curl 会读 IE 设置所以"看起来能通"）。

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// systemProxyURL 读系统代理（Internet Settings：ProxyEnable/ProxyServer）。
// 返回 "host:port"；未启用/读取失败=空（调用方退环境变量/直连）。
func systemProxyURL() string {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	if enable, _, err := k.GetIntegerValue("ProxyEnable"); err != nil || enable == 0 {
		return ""
	}
	srv, _, err := k.GetStringValue("ProxyServer")
	if err != nil || srv == "" {
		return ""
	}
	// 两种格式："host:port" 或 "http=h:p;https=h:p;ftp=..."（优先 https 项）。
	if strings.Contains(srv, "=") {
		best := ""
		for _, part := range strings.Split(srv, ";") {
			part = strings.TrimSpace(part)
			if v, ok := strings.CutPrefix(part, "https="); ok {
				return v
			}
			if v, ok := strings.CutPrefix(part, "http="); ok && best == "" {
				best = v
			}
		}
		return best
	}
	return srv
}
