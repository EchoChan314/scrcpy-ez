package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Settings 是 GUI 全局设置（与设备档案 profiles.json 分离，独立落盘 settings.json）。
// 两个开关都是"壳"级行为，与具体设备无关，因此不放进设备档案：
//
//	ShowParamOverlay 开关 A：启动投屏时显示参数控件（投屏窗口上的 fps/码率/状态浮层）。
//	                 true=默认可见（历史行为）；false=默认隐藏，投屏中 Ctrl+F 仍可手动切换。
//	CloseToTray     开关 B：关闭窗口时最小化到托盘。
//	                 true=点窗口关闭按钮只隐藏窗口（进程存活、投屏不中断）；
//	                 false=完整退出（历史行为）。
//
// 两个开关都有明确默认值，缺失键=默认（不因档案残缺改变现状行为）。
type Settings struct {
	ShowParamOverlay bool `json:"showParamOverlay"`
	CloseToTray      bool `json:"closeToTray"`
}

// DefaultSettings 返回出厂默认：参数控件显示（现状不变）、关闭窗口完整退出（现状不变）。
func DefaultSettings() Settings {
	return Settings{ShowParamOverlay: true, CloseToTray: false}
}

// settingsFile 是 settings.json 的落盘形状：指针字段区分"键缺失"（用默认值）
// 与"显式 false"——否则旧文件里缺的键会被零值 false 覆盖掉默认 true。
type settingsFile struct {
	ShowParamOverlay *bool `json:"showParamOverlay"`
	CloseToTray      *bool `json:"closeToTray"`
}

// SettingsStore 持久化全局设置（独立文件，绝不写进 profiles.json）。
// 默认路径 = 与 profiles.json 同目录的 settings.json（软件目录优先，受限位回退
// %APPDATA%\scrcpy-ez\，见 main_windows.go）；path 为空=内存模式（不落盘，测试用）。
type SettingsStore struct {
	path string
	mu   sync.Mutex
	data Settings
}

func NewSettingsStore(path string) *SettingsStore {
	return &SettingsStore{path: path, data: DefaultSettings()}
}

// Load 读盘：文件缺失/损坏/键缺失都回落到默认值（不阻断启动，与 ProfileStore 同口径）。
func (s *SettingsStore) Load() error {
	if s.path == "" {
		return nil
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(b) == 0 {
		return nil
	}
	var f settingsFile
	if err := json.Unmarshal(b, &f); err != nil {
		// 损坏文件：保持默认值（宁可回到默认，也不要让 GUI 起不来）
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = DefaultSettings()
	if f.ShowParamOverlay != nil {
		s.data.ShowParamOverlay = *f.ShowParamOverlay
	}
	if f.CloseToTray != nil {
		s.data.CloseToTray = *f.CloseToTray
	}
	return nil
}

// Get 取当前设置（两个开关的只读快照）。
func (s *SettingsStore) Get() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data
}

// Set 全量写入两个开关并立即落盘（原子写：tmp+rename）。
// 落盘失败时内存值保持已更新（本次会话生效），错误上抛给前端提示。
func (s *SettingsStore) Set(showParamOverlay, closeToTray bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = Settings{ShowParamOverlay: showParamOverlay, CloseToTray: closeToTray}
	return s.persistLocked()
}

// persistLocked 落盘（调用方必须持锁）。
func (s *SettingsStore) persistLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
