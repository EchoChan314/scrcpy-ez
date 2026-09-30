//go:build !windows

package app

// systemProxyURL 非 Windows 桩（环境变量代理由 http.ProxyFromEnvironment 兜底）。
func systemProxyURL() string { return "" }
