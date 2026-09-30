package updater

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type State struct {
	Phase      string      `json:"phase"`
	Info       ReleaseInfo `json:"info"`
	Downloaded int64       `json:"downloaded"`
	Total      int64       `json:"total"`
	Speed      int64       `json:"speed"`
	Message    string      `json:"message"`
	Error      string      `json:"error,omitempty"`
	CanInstall bool        `json:"canInstall"`
	Result     *Result     `json:"result,omitempty"`
	Source     string      `json:"source,omitempty"`
}

type Options struct {
	Install       string
	Cache         string
	Version       string
	Client        *http.Client
	DirectClient  *http.Client
	Fetch         func(context.Context) (ReleaseInfo, error)
	AllowTestHTTP bool
}

type cached struct {
	Info  ReleaseInfo `json:"info"`
	Plan  Plan        `json:"plan"`
	Ready bool        `json:"ready"`
}
type Manager struct {
	mu     sync.Mutex
	opts   Options
	state  State
	cancel context.CancelFunc
	done   chan struct{}
	cache  cached
	closed bool
}

func New(opts Options) *Manager {
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if opts.Fetch == nil {
		opts.Fetch = func(ctx context.Context) (ReleaseInfo, error) {
			return FetchWithClients(ctx, opts.Client, opts.DirectClient, opts.Version)
		}
	}
	m := &Manager{opts: opts, state: State{Phase: "idle", Info: ReleaseInfo{Current: opts.Version, RepoURL: RepoURL, DownloadURL: LatestURL}}}
	if opts.Cache != "" {
		var c cached
		if ReadJSON(filepath.Join(opts.Cache, "candidate.json"), &c) == nil && c.Plan.Install == opts.Install && VersionLess(opts.Version, c.Info.Latest) && filepath.Dir(c.Plan.Work) == opts.Cache && len(c.Plan.Token) == 32 && filepath.Base(c.Plan.Work) == "job-"+c.Plan.Token {
			m.cache = c
			c.Info.Current = opts.Version
			c.Info.HasNew = true
			m.state.Info = c.Info
			if c.Ready {
				m.state.Phase, m.state.CanInstall, m.state.Message = "ready", true, "更新已准备好，可重启安装"
			}
		}
	}
	return m
}

func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.state
	st.Info.Sources = append([]DownloadSource(nil), st.Info.Sources...)
	var result Result
	if ReadJSON(filepath.Join(m.opts.Install, ".ez-update-result.json"), &result) == nil {
		st.Result = &result
	}
	return st
}
func (m *Manager) busyLocked() bool {
	switch m.state.Phase {
	case "checking", "selecting", "downloading", "validating", "installing":
		return true
	}
	return false
}
func (m *Manager) change(fn func(*State)) { m.mu.Lock(); fn(&m.state); m.mu.Unlock() }

func (m *Manager) Check() State {
	m.mu.Lock()
	if m.closed || m.busyLocked() || m.state.CanInstall {
		st := m.state
		m.mu.Unlock()
		return st
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	m.cancel = cancel
	done := make(chan struct{})
	m.done = done
	m.state.Phase, m.state.Message, m.state.Error = "checking", "正在检查更新…", ""
	m.state.Source = ""
	st := m.state
	m.mu.Unlock()
	go func() {
		defer close(done)
		defer cancel()
		info, err := m.opts.Fetch(ctx)
		m.change(func(s *State) {
			if ctx.Err() != nil && info.Latest == "" {
				err = ctx.Err()
			}
			s.Info = info
			if err != nil {
				s.Phase, s.Error, s.Message = "error", err.Error(), "检查失败，可重试或打开仓库页面"
				s.Info.Error = err.Error()
			} else {
				s.Phase = "available"
				if info.HasNew {
					s.Message = "发现新版本，可以下载更新"
				} else if VersionLess(info.Latest, m.opts.Version) {
					s.Message = "当前版本高于公开发行版本"
				} else {
					s.Message = "已是最新版本"
					if info.Notice != "" {
						s.Message = "当前可访问通道未发现新版本"
					}
				}
			}
		})
	}()
	return st
}

func (m *Manager) Download() error {
	m.mu.Lock()
	if m.closed || m.busyLocked() {
		m.mu.Unlock()
		return fmt.Errorf("更新任务正在进行")
	}
	if m.state.CanInstall {
		m.mu.Unlock()
		return nil
	}
	info := m.state.Info
	if !info.HasNew || !VersionLess(m.opts.Version, info.Latest) || len(m.candidates(info)) == 0 {
		m.mu.Unlock()
		return fmt.Errorf("没有可下载的新版本，请先检查更新")
	}
	if m.opts.Cache == "" {
		m.mu.Unlock()
		return fmt.Errorf("更新缓存目录不可用")
	}
	if err := CheckWritable(m.opts.Install); err != nil {
		m.mu.Unlock()
		return err
	}
	c := m.cache
	// Retain a previously learned digest when a later check can only reach the mirror.
	if sameVersion(c.Info.Latest, info.Latest) && info.Digest == "" && sha256Digest.MatchString(c.Info.Digest) && (info.Size == 0 || c.Info.Size == 0 || info.Size == c.Info.Size) {
		info.Digest = c.Info.Digest
		if info.Size == 0 {
			info.Size = c.Info.Size
		}
	}
	if !packageIdentity(c.Info, info) {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			m.mu.Unlock()
			return err
		}
		token := hex.EncodeToString(b)
		c = cached{Info: info, Plan: Plan{Token: token, Version: info.Latest, Install: m.opts.Install, Work: filepath.Join(m.opts.Cache, "job-"+token)}}
	}
	c.Info = info
	m.cache = c
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	done := make(chan struct{})
	m.done = done
	m.state.Phase, m.state.Error, m.state.Message, m.state.CanInstall = "selecting", "", "正在选择快速可用的下载源…", false
	m.state.Source = "自动选择"
	m.state.Total, m.state.Downloaded, m.state.Speed = info.Size, 0, 0
	m.mu.Unlock()
	go func() {
		defer close(done)
		defer cancel()
		err := m.download(ctx, &c)
		m.mu.Lock()
		defer m.mu.Unlock()
		m.cache = c
		if err != nil {
			m.state.CanInstall = false
			if ctx.Err() != nil {
				m.state.Phase, m.state.Message, m.state.Error = "canceled", "下载已取消，已下载部分保留", ""
			} else {
				m.state.Phase, m.state.Message, m.state.Error = "error", "下载未完成，可重试", err.Error()
			}
			return
		}
		m.cache = c
		m.state.Phase, m.state.Message, m.state.Error, m.state.CanInstall = "ready", "更新已准备好，设备档案与设置会保留", "", true
	}()
	return nil
}

func (m *Manager) Cancel() {
	m.mu.Lock()
	if m.state.Phase == "selecting" || m.state.Phase == "downloading" || m.state.Phase == "validating" {
		if m.cancel != nil {
			m.cancel()
		}
	}
	m.mu.Unlock()
}
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
}

func (m *Manager) PrepareInstall() (Plan, error) {
	m.mu.Lock()
	if m.closed || !m.state.CanInstall || m.busyLocked() {
		m.mu.Unlock()
		return Plan{}, fmt.Errorf("更新尚未准备好")
	}
	p := m.cache.Plan
	m.state.Phase, m.state.Message = "installing", "正在准备重启更新…"
	m.mu.Unlock()
	if err := p.Validate(); err != nil {
		m.mu.Lock()
		m.cache.Ready = false
		m.state.CanInstall = false
		c := m.cache
		m.mu.Unlock()
		_ = WriteJSON(filepath.Join(m.opts.Cache, "candidate.json"), c)
		m.InstallError(err)
		return Plan{}, err
	}
	if err := CheckWritable(p.Install); err != nil {
		m.InstallError(err)
		return Plan{}, err
	}
	return p, nil
}
func (m *Manager) InstallError(err error) {
	m.change(func(s *State) {
		s.Phase, s.Error, s.Message = "ready", err.Error(), "安装未开始，可稍后重试"
		if !s.CanInstall {
			s.Phase, s.Message = "error", "更新暂存文件不可用，请重新下载"
		}
	})
}
func (m *Manager) DismissResult() {
	_ = os.Remove(filepath.Join(m.opts.Install, ".ez-update-result.json"))
}

func CacheDir(install string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(strings.ToLower(install)))
	return filepath.Join(base, "scrcpy-ez", "updates", hex.EncodeToString(sum[:8])), nil
}
