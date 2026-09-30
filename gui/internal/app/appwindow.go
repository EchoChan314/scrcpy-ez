package app

// 应用窗口（二期）· Step 1：应用列表（枚举 + 就绪触发 + 入档）。
//
// 【背景】task_vd_appwindow_0919（总纲）+ 主人在线拍板（0919 晚）：
//   - 应用列表是"应用窗口"功能的数据底座（Step 2 面板 / Step 5 应用档案都靠它）；
//   - 枚举时机 = 设备"稳定就绪"边沿（配对完成 / 离线卡→在线卡），不追 adb 早期瞬态；
//   - 图标与列表同批交付（Step 1b：server 导出 + adb pull）——本文件先落列表部分；
//   - 结果入档（设备 identity 键）——离线可读、切换连接不重枚举；
//   - 抗打断（插线学习等会瞬时打断 adb）：失败静默 + 重试一次，不影响任何会话。

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
)

const (
	appListTTL      = 5 * time.Minute  // 读缓存保鲜期
	appListTimeout  = 20 * time.Second // 单次 --list-apps 超时（实测 ~5s，留 4x 余量）
	appListRetries  = 1                // 失败重试次数（额外的）
	appListRetryGap = 6 * time.Second  // 重试间隔（跨 adbd 重启窗口）
	appListMaxApps  = 500              // 解析上限（防异常输出）
	appListBadgeTTL = 10 * time.Second // 「应用」按钮「读取中…」遮罩最长显示（兜底：枚举重试中到时也清）
	appListUsbWaitWindow = 2500 * time.Millisecond // 枚举前"等 USB 出现"窗口（v2.1.21 修复：启动瞬间无线卡先到，USB 晚 ~2s）
)

// AppListItem 是应用列表一项（`scrcpy --list-apps` 解析结果）。
type AppListItem struct {
	Pkg  string `json:"pkg"`
	Name string `json:"name"`
	Sys  bool   `json:"sys"` // * = 系统应用 / - = 第三方
}

// appListEntry 应用列表缓存项（内存态；持久层在档案 profiles.json）。
type appListEntry struct {
	items []AppListItem
	at    time.Time
}

// ---------- 就绪边沿触发 ----------

// devicesWithAppBusyLocked 复制设备列表并填充「应用」按钮的枚举遮罩态（AppBusy）。
// 调用方必须已持 a.mu（snapshotRaw 的锁内调用）。遮罩=该 identity 正在枚举
// 且未超 10s 兜底（枚举完毕清 busy → 下一步快照即消失；重试超时也最多显示 10s）。
func (a *App) devicesWithAppBusyLocked() []adb.Device {
	devs := append([]adb.Device{}, a.devices...)
	for i := range devs {
		id := devs[i].Identity
		if id == "" {
			continue
		}
		if ts, ok := a.appListBusy[id]; ok && time.Since(ts) < appListBadgeTTL && !a.appListSilent[id] {
			devs[i].AppBusy = true
		}
	}
	return devs
}

// kickAppListOnReadyChange 检查"就绪边沿"：某设备（identity）从"未就绪"变为
// "就绪"（State==device 且非 Connecting/Pairing 合成卡）时触发一次后台枚举。
//
// 【语义】（主人 0919 拍板）：设备"稳定"的两个入口——配对完成 / 离线卡→在线卡——
// 在显示层都表现为"该 identity 的就绪翻转"；本函数挂在 commitDisplay 末尾（显示
// 提交后），一处覆盖全部场景。多 transport 卡（USB+无线）按 identity 聚合（任一
// 就绪=设备就绪），避免顺序抖动导致重复触发。
func (a *App) kickAppListOnReadyChange(devs []adb.Device) {
	type job struct{ identity, serial string }
	var jobs []job

	a.mu.Lock()
	if a.appListLastReady == nil {
		a.appListLastReady = map[string]bool{}
	}
	if a.appListBusy == nil {
		a.appListBusy = map[string]time.Time{}
	}
	if a.appListSilentChanged == nil {
		a.appListSilentChanged = map[string]bool{}
	}
	// 聚合：每 identity 的"就绪"（任一 transport 卡就绪）+ 一个可用 serial。
	readyMap := map[string]bool{}
	serialOf := map[string]string{}
	for i := range devs {
		d := &devs[i]
		if d.Identity == "" || d.Serial == "" {
			continue
		}
		if d.State == "device" && !d.Connecting && !d.Pairing {
			readyMap[d.Identity] = true
			// serial 选取（v2.1.21 修复）：优先 USB——无线 transport 可能是
			// stale 档案地址（换网/关闭无线调试后该 transport 仍显示 device
			// 但实际 connect 超时）→ 曾出现"GUI 重启后应用列表读不出（枚举
			// 超时）"。USB 物理连接必通；首个就绪卡兜底。
			cur, ok := serialOf[d.Identity]
			if !ok || (strings.Contains(cur, ":") && !strings.Contains(d.Serial, ":")) {
				serialOf[d.Identity] = d.Serial
			}
		} else if _, ok := readyMap[d.Identity]; !ok {
			readyMap[d.Identity] = false
		}
	}
	for id, ready := range readyMap {
		if ready && !a.appListLastReady[id] {
			if _, busy := a.appListBusy[id]; !busy {
				a.appListBusy[id] = time.Now() // 值=开始时刻（「应用」按钮遮罩 10s 兜底用）
				a.appListSilentChanged[id] = false
				jobs = append(jobs, job{identity: id, serial: serialOf[id]})
			}
		}
		a.appListLastReady[id] = ready
	}
	// 清掉已消失设备的记录（防 map 无限增长）。
	for k := range a.appListLastReady {
		if _, ok := readyMap[k]; !ok {
			delete(a.appListLastReady, k)
		}
	}
	a.mu.Unlock()

	for _, j := range jobs {
		bridge.DebugLog("[appwin] 就绪边沿 → 应用列表枚举 serial=%q identity=%q", j.serial, j.identity)
		go a.runAppListEnum(j.identity, j.serial)
		// v2.1.32：虚拟屏 dpi 参数预热（后台）——设备就绪时顺手把 wm size/density
		// 查好缓存，开窗路径只读缓存（GUI 消息循环线程不被慢查询冻结）。
		go a.guard("appwin-phys-warm", func() { a.devicePhys(j.serial) })
	}
}

// bestEnumSerial 复核该 identity 当前最佳的枚举 serial（USB 优先）：
// 无线 transport 可能是 stale 档案地址（换网/无线调试关闭后仍显示 device
// 但 connect 超时）；USB 物理连接必通。未找到时用 fallback。
func (a *App) bestEnumSerial(identity, fallback string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	wifi := ""
	for i := range a.devices {
		d := &a.devices[i]
		if d.Identity != identity || d.State != "device" || d.Connecting || d.Pairing || d.Serial == "" {
			continue
		}
		if !strings.Contains(d.Serial, ":") {
			return d.Serial // USB 最优
		}
		if wifi == "" {
			wifi = d.Serial
		}
	}
	if wifi != "" {
		return wifi
	}
	return fallback
}

// waitBestEnumSerial 在"USB 可能尚未出现"的窗口内短暂等待（每 250ms 复核）：
// 拿到 USB 立即返回；超时返回当前最佳。纯无线设备最多多等 timeout。
func (a *App) waitBestEnumSerial(identity, fallback string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	best := a.bestEnumSerial(identity, fallback)
	for strings.Contains(best, ":") && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
		if s := a.bestEnumSerial(identity, fallback); s != best {
			best = s
			if !strings.Contains(best, ":") {
				return best
			}
		}
	}
	return best
}

// appListDiff 计算两次应用列表的差异（pkg 集合 + 名称映射；顺序无关）。
// v2.1.16 列表 diff（联合判据·列表侧）：same 且未超兜底期 → 跳过图标导出。
func appListDiff(oldList, cur []AppListItem) (same bool, added, removed, renamed []string) {
	om := make(map[string]string, len(oldList))
	for _, e := range oldList {
		om[e.Pkg] = e.Name
	}
	cm := make(map[string]string, len(cur))
	for _, e := range cur {
		cm[e.Pkg] = e.Name
	}
	for pkg, name := range cm {
		if oldName, ok := om[pkg]; !ok {
			added = append(added, pkg)
		} else if oldName != name {
			renamed = append(renamed, pkg)
		}
	}
	for pkg := range om {
		if _, ok := cm[pkg]; !ok {
			removed = append(removed, pkg)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(renamed)
	same = len(added) == 0 && len(removed) == 0 && len(renamed) == 0
	return
}

// appListUpdateRequired 入口静默检测仅在有差异时提交；设备就绪枚举继续沿用原流程。
func appListUpdateRequired(silentDiff, same bool) bool {
	return !silentDiff || !same
}

// runAppListEnum 后台执行一次枚举（含重试）→ 内存缓存 + 档案落盘 → diff 决策 → 图标导出。
// v2.1.16 列表 diff：
//   - 列表无变化 且 距上次全量 < 24h → 跳过图标导出（push/export/pull 全免，遮罩秒清）；
//   - 仅卸载（无新增/改名）→ 只删本地 PNG，不起 server；
//   - 有新增/改名（且全量未超期、变化数 ≤50）→ 定向导出（只导变化的包）；
//   - 首次/超期/变化过大 → 全量（清目录重导，并记录全量时间）。
// busy 联合判据（主人 0919）：枚举成功后 busy 交接给图标流程，图标入库才清；
// 跳过/仅删/任一步失败=立即清（失败静默，下次稳定期重试）。
func (a *App) runAppListEnum(identity, serial string) {
	a.runAppListEnumMode(identity, serial, false)
}

// runAppListEnumMode silentDiff=true 用于入口点击检测：只有与最新缓存存在差异时才
// 写入列表/档案并继续图标流程；无差异时不更新时间戳、不触发任何 UI 更新。
func (a *App) runAppListEnumMode(identity, serial string, silentDiff bool) {
	if !silentDiff {
		a.mu.Lock()
		if !a.appListSilent[identity] {
			if a.appListSilentChanged == nil {
				a.appListSilentChanged = map[string]bool{}
			}
			a.appListSilentChanged[identity] = false
		}
		a.mu.Unlock()
	}
	// v2.1.84：等 adb 服务就绪（抢庄检测完成）后再枚举——"应用名+图标获取也要
	// 等 server 确定好了再开始"（此前可能撞上服务重建窗口 → 枚举超时白跑）。
	a.waitSrvReady(context.Background())
	// serial 复核（v2.1.21 修复）：枚举触发瞬间可能只有无线卡可见（USB 卡晚 ~2s
	// 出现），而无线 transport 可能是 stale 档案地址（枚举必超时）——开工前短暂
	// 等待 USB 出现（纯无线设备最多多等 appListUsbWaitWindow）。
	serial = a.waitBestEnumSerial(identity, serial, appListUsbWaitWindow)
	var items []AppListItem
	var err error
	for attempt := 0; attempt <= appListRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(appListRetryGap)
			serial = a.bestEnumSerial(identity, serial) // 重试前复核（USB 优先）
		}
		items, err = listAppsOnce(a.scrcpyExePath(), serial)
		if err == nil {
			break
		}
		bridge.DebugLog("[appwin] 应用列表枚举尝试%d失败 identity=%q serial=%q: %v", attempt+1, identity, serial, err)
	}
	if err != nil {
		// 失败静默：不弹错、不打扰；下一次稳定期（或手动刷新）自然重试。
		a.clearAppBusy(identity)
		bridge.DebugLog("[appwin] 应用列表枚举失败 identity=%q serial=%q: %v", identity, serial, err)
		return
	}

	// 读档案旧列表 + 上次全量时间（diff 判据）。
	var oldApps []AppListItem
	var iconsFullAt int64
	if e, ok := a.profiles.Entry(identity); ok {
		oldApps = e.Apps
		iconsFullAt = e.IconsFullAt
	}
	if silentDiff {
		// 点击检测以最近一次内存列表为准；进程重启或缓存缺失时回退档案。
		a.mu.RLock()
		if cached, ok := a.appListCache[identity]; ok && time.Since(cached.at) < appListTTL {
			oldApps = cached.items
		}
		a.mu.RUnlock()
	}
	same, added, removed, renamed := appListDiff(oldApps, items)
	a.markAppListCheckChanged(identity, !same)
	if !appListUpdateRequired(silentDiff, same) {
		a.clearAppBusy(identity)
		bridge.DebugLog("[appwin] 点击检测无差异 identity=%q，保持列表与档案不变", identity)
		return
	}
	fresh := iconsFullAt > 0 && time.Since(time.Unix(iconsFullAt, 0)) < appIconsFullTTL

	a.mu.Lock()
	if a.appListCache == nil {
		a.appListCache = map[string]appListEntry{}
	}
	a.appListCache[identity] = appListEntry{items: items, at: time.Now()}
	a.mu.Unlock()
	if err := a.profiles.SetApps(identity, items); err != nil {
		a.clearAppBusy(identity)
		bridge.DebugLog("[appwin] 应用列表入档失败 identity=%q: %v", identity, err)
		return
	}
	bridge.DebugLog("[appwin] 应用列表已入档 identity=%q：n=%d（diff: +%d -%d ~%d，全量超期=%v）",
		identity, len(items), len(added), len(removed), len(renamed), !fresh)

	// ① 列表无变化且全量未超期 → 跳过图标导出（v2.1.16：日常插拔的主要快路径）。
	if same && fresh {
		a.clearAppBusy(identity)
		bridge.DebugLog("[appwin] 列表无变化且未超期，跳过图标导出 identity=%q", identity)
		return
	}

	// ② 仅卸载（无新增/改名）→ 只删本地 PNG，不起 server（秒级）。
	if fresh && !same && len(added)+len(renamed) == 0 {
		iconsDir := a.iconsDirFor(identity)
		for _, pkg := range removed {
			if err := os.Remove(filepath.Join(iconsDir, pkg+".png")); err != nil && !os.IsNotExist(err) {
				bridge.DebugLog("[appwin] 删除本地图标失败 identity=%q pkg=%q: %v", identity, pkg, err)
			}
		}
		a.clearAppBusy(identity)
		bridge.DebugLog("[appwin] 仅有卸载变化，本地删除完成 identity=%q（removed=%d）", identity, len(removed))
		return
	}

	// ③ 定向（新增/改名 ≤50 且未超期）或全量。
	var only []string
	mode := "全量"
	if fresh && !same && len(added)+len(renamed) <= appIconsDirectMax {
		only = make([]string, 0, len(added)+len(renamed))
		only = append(only, added...)
		only = append(only, renamed...)
		mode = "定向"
	}
	bridge.DebugLog("[appwin] 图标导出模式=%s identity=%q（only=%d removed=%d）",
		mode, identity, len(only), len(removed))

	// Step 1c：列表就绪 → 接图标导出（busy 由图标流程接管并最终清理）。
	a.runAppIcons(identity, serial, only, removed)
}

// clearAppBusy 清「应用」按钮遮罩态（幂等）。
func (a *App) clearAppBusy(identity string) {
	a.mu.Lock()
	delete(a.appListBusy, identity)
	delete(a.appListSilent, identity)
	a.mu.Unlock()
}

func (a *App) markAppListCheckChanged(identity string, changed bool) {
	a.mu.Lock()
	if a.appListSilentChanged == nil {
		a.appListSilentChanged = map[string]bool{}
	}
	a.appListSilentChanged[identity] = changed
	a.mu.Unlock()
}

// touchAppBusy 遮罩心跳续期（有进展=续 10s；已被兜底过期则不复活）。
func (a *App) touchAppBusy(identity string) {
	a.mu.Lock()
	if _, ok := a.appListBusy[identity]; ok {
		a.appListBusy[identity] = time.Now()
	}
	a.mu.Unlock()
}

// ---------- 二期 Step 1c：图标导出与入库 ----------

const (
	iconExportTimeout = 90 * time.Second // 起 server 导出图标（实测 ~6s，大余量防老设备）
	iconPushTimeout   = 30 * time.Second // push server
	iconPullTimeout   = 60 * time.Second // pull 图标目录
	// v2.1.16 列表 diff：距上次全量图标导出超过此时长 → 强制全量（兜底抓"图标变了但列表没变"）。
	appIconsFullTTL = 24 * time.Hour
	// v2.1.16 定向上限：新增+改名超过此数改走全量（定向逐文件 pull 的成本拐点附近）。
	appIconsDirectMax = 50
	serverVersion     = "4.1"            // 与 dist/scrcpy-server 一致（随构建同步）
)

// runAppIcons 后台导出应用图标：push server → 起 server（export_app_icons）→ pull 入库。
// v2.1.16：onlyPkgs == nil → 全量（清本地目录 + 全量 pull + 记全量时间）；
// onlyPkgs 非 nil → 定向（server 只导这些包 + 逐文件 pull；removed 从本地删除；不动其他）。
// busy 心跳：每阶段完成续 10s；结束（成功/失败）清 busy（遮罩消失）。
// 与 scrcpy client 解耦：直接 adb 起我们自定义的 server（免 client 改动）。
func (a *App) runAppIcons(identity, serial string, onlyPkgs []string, removedPkgs []string) {
	defer a.clearAppBusy(identity)

	distDir := filepath.Dir(a.cfg.BatPath)
	serverPath := filepath.Join(distDir, "scrcpy-server")
	iconsDir := a.iconsDirFor(identity)
	t0 := time.Now()
	mode := "全量"
	if onlyPkgs != nil {
		mode = "定向"
	}

	// 1) push server（无脑重推；USB 毫秒级 / 无线 ~1s）
	pushCtx, pushCancel := context.WithTimeout(context.Background(), iconPushTimeout)
	err := a.adb.PushFile(pushCtx, serial, serverPath, "/data/local/tmp/scrcpy-server")
	pushCancel()
	if err != nil {
		bridge.DebugLog("[appwin] 图标导出·push 失败 identity=%q serial=%q: %v", identity, serial, err)
		return
	}
	t1 := time.Now()
	a.touchAppBusy(identity)

	// 2) 起 server 导出（one-shot；成功输出含 "Exported icons: N"）
	ctx, cancel := context.WithTimeout(context.Background(), iconExportTimeout)
	defer cancel()
	exportArg := "export_app_icons=true"
	if onlyPkgs != nil {
		exportArg = "export_app_icons=" + strings.Join(onlyPkgs, ",")
	}
	shellCmd := "CLASSPATH=/data/local/tmp/scrcpy-server app_process / com.genymobile.scrcpy.Server " +
		serverVersion + " " + exportArg
	out, err := a.adb.ShellOut(ctx, serial, shellCmd)
	if err != nil || !strings.Contains(out, "Exported icons:") {
		bridge.DebugLog("[appwin] 图标导出·server 失败 identity=%q serial=%q err=%v out=%q",
			identity, serial, err, strings.TrimSpace(out))
		return
	}
	// server 端细分计时（ICON_TIMING）顺带入日志。
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "ICON_TIMING") {
			bridge.DebugLog("[appwin] server %s", strings.TrimSpace(line))
		}
	}
	t2 := time.Now()
	a.touchAppBusy(identity)

	if onlyPkgs != nil {
		// 3a) 定向：逐文件 pull（N 小）+ removed 本地删除；不动其他图标。
		if err := os.MkdirAll(iconsDir, 0o755); err != nil {
			bridge.DebugLog("[appwin] 图标导出·建目录失败 identity=%q dir=%q: %v", identity, iconsDir, err)
			return
		}
		for _, pkg := range onlyPkgs {
			pullCtx, pullCancel := context.WithTimeout(context.Background(), iconPullTimeout)
			err = a.adb.PullFile(pullCtx, serial,
				"/data/local/tmp/scrcpy/icons/"+pkg+".png",
				filepath.Join(iconsDir, pkg+".png"))
			pullCancel()
			if err != nil {
				// 失败静默：单个图标失败不致命（兜底全量会重抓）。
				bridge.DebugLog("[appwin] 图标导出·定向 pull 失败 identity=%q pkg=%q: %v", identity, pkg, err)
			}
		}
		for _, pkg := range removedPkgs {
			if err := os.Remove(filepath.Join(iconsDir, pkg+".png")); err != nil && !os.IsNotExist(err) {
				bridge.DebugLog("[appwin] 图标导出·删除本地失败 identity=%q pkg=%q: %v", identity, pkg, err)
			}
		}
	} else {
		// 3b) 全量：清本地旧图标 + pull（全量覆盖=与设备一致；卸载应用的残留自然清）
		if err := os.RemoveAll(iconsDir); err != nil {
			bridge.DebugLog("[appwin] 图标导出·清理本地失败 identity=%q dir=%q: %v", identity, iconsDir, err)
			return
		}
		if err := os.MkdirAll(iconsDir, 0o755); err != nil {
			bridge.DebugLog("[appwin] 图标导出·建目录失败 identity=%q dir=%q: %v", identity, iconsDir, err)
			return
		}
		pullCtx, pullCancel := context.WithTimeout(context.Background(), iconPullTimeout)
		err = a.adb.PullDir(pullCtx, serial, "/data/local/tmp/scrcpy/icons/.", iconsDir)
		pullCancel()
		if err != nil {
			bridge.DebugLog("[appwin] 图标导出·pull 失败 identity=%q serial=%q: %v", identity, serial, err)
			return
		}
		// 全量完成 → 记录时间（列表 diff 的兜底判据）。
		if err := a.profiles.SetIconsFullAt(identity, time.Now().Unix()); err != nil {
			bridge.DebugLog("[appwin] 图标全量时间落档失败 identity=%q: %v", identity, err)
		}
	}

	bridge.DebugLog("[appwin] 图标分段计时 identity=%q：push=%dms export=%dms pull=%dms total=%dms（%s）",
		identity, t1.Sub(t0).Milliseconds(), t2.Sub(t1).Milliseconds(),
		time.Since(t2).Milliseconds(), time.Since(t0).Milliseconds(), mode)
	bridge.DebugLog("[appwin] 应用图标已入库 identity=%q：n=%d dir=%q", identity, countPNGs(iconsDir), iconsDir)
}

// countPNGs 数目录内 .png 文件（日志用）。
func countPNGs(dir string) int {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".png") {
			n++
		}
	}
	return n
}

// GetAppIcon 读应用图标（data URL；找不到=空串，前端回退彩块）。
// "满射"口径：每个应用都查得到结果（有 PNG 给 PNG，无则前端兜底）。
func (a *App) GetAppIcon(serial, pkg string) (string, error) {
	if pkg == "" || !rePkgName.MatchString(pkg) {
		return "", nil // 非法包名（顺带防路径穿越）
	}
	key := a.appListKeyFor(serial)
	if key == "" {
		return "", nil
	}
	b, err := os.ReadFile(filepath.Join(a.iconsDirFor(key), pkg+".png"))
	if err != nil {
		return "", nil
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b), nil
}

// iconsDirFor 图标缓存目录：<档案目录>/icons/<identity 安全化>/（跟设备、跟软件目录）。
func (a *App) iconsDirFor(identity string) string {
	return filepath.Join(filepath.Dir(a.profiles.Path()), "icons", sanitizeFileName(identity))
}

// sanitizeFileName 去掉 Windows 文件名非法字符（空格保留，保持可读）。
func sanitizeFileName(s string) string {
	s = strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_").Replace(s)
	s = strings.TrimRight(s, ". ")
	if s == "" {
		s = "device"
	}
	return s
}

// ---------- 执行与解析 ----------

func (a *App) scrcpyExePath() string {
	return filepath.Join(filepath.Dir(a.cfg.BatPath), "scrcpy.exe")
}

// listAppsWithRetry 跑 --list-apps（失败重试；间隔跨 adbd 重启窗口）。
func listAppsWithRetry(exe, serial string) ([]AppListItem, error) {
	var lastErr error
	for attempt := 0; attempt <= appListRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(appListRetryGap)
		}
		items, err := listAppsOnce(exe, serial)
		if err == nil {
			return items, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// listAppsOnce 单次执行（带超时；超时即杀，不留残余）。
func listAppsOnce(exe, serial string) ([]AppListItem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), appListTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--serial", serial, "--list-apps")
	adb.HideConsole(cmd) // GUI 无控制台：禁止子进程新建控制台窗口（防闪窗）
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, errors.New("读取应用列表超时")
	}
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("读取应用列表失败: %w", err)
	}
	items := ParseAppList(string(out))
	if len(items) == 0 {
		return nil, errors.New("未解析到任何应用（设备可能未就绪）")
	}
	if len(items) > appListMaxApps {
		items = items[:appListMaxApps]
	}
	return items, nil
}

var rePkgName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+$`)

// ParseAppList 解析 `scrcpy --list-apps` 输出（纯函数，便于单测）：
//
//   - <名称（30 列对齐，含中文）>  <包名>
//   - <名称>                       <包名>
//
// 末尾 token=包名（形如 a.b.c，正则校验），其余为展示名（多空格折叠）。
func ParseAppList(out string) []AppListItem {
	var items []AppListItem
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimLeft(line, " \t")
		var sys bool
		switch {
		case strings.HasPrefix(trimmed, "* "):
			sys = true
		case strings.HasPrefix(trimmed, "- "):
			sys = false
		default:
			continue
		}
		body := strings.TrimSpace(trimmed[2:])
		fields := strings.Fields(body)
		if len(fields) < 2 {
			continue
		}
		pkg := fields[len(fields)-1]
		if !rePkgName.MatchString(pkg) || seen[pkg] {
			continue
		}
		name := strings.TrimSpace(strings.TrimSuffix(body, pkg))
		if name == "" {
			name = pkg
		}
		seen[pkg] = true
		items = append(items, AppListItem{Pkg: pkg, Name: name, Sys: sys})
	}
	return items
}

// ---------- RPC 支撑（Step 2 面板使用；此处先备好） ----------

// GetAppList 读应用列表：内存缓存（TTL 内）→ 档案回退；都没有=空数组（前端可触发刷新）。
func (a *App) GetAppList(serial string) ([]AppListItem, error) {
	key := a.appListKeyFor(serial)
	if key == "" {
		return nil, errors.New("未指定设备")
	}
	a.mu.RLock()
	if it, ok := a.appListCache[key]; ok && time.Since(it.at) < appListTTL {
		items := it.items
		a.mu.RUnlock()
		return items, nil
	}
	a.mu.RUnlock()
	if e, ok := a.profiles.Entry(key); ok && len(e.Apps) > 0 {
		return e.Apps, nil
	}
	return []AppListItem{}, nil
}

// RefreshAppList 手动刷新（前端按钮）：异步跑一次（busy 防重），立即返回。
func (a *App) RefreshAppList(serial string) error {
	key := a.appListKeyFor(serial)
	if key == "" {
		return errors.New("未指定设备")
	}
	a.mu.Lock()
	if a.appListBusy == nil {
		a.appListBusy = map[string]time.Time{}
	}
	if _, busy := a.appListBusy[key]; busy {
		a.mu.Unlock()
		return nil // 已在跑：幂等
	}
	a.appListBusy[key] = time.Now()
	a.resetAppListCheckResultLocked(key)
	a.mu.Unlock()
	go a.runAppListEnum(key, serial)
	return nil
}

func (a *App) resetAppListCheckResultLocked(key string) {
	if a.appListSilentChanged == nil {
		a.appListSilentChanged = map[string]bool{}
	}
	a.appListSilentChanged[key] = false
}

// beginAppListCheckLocked 复用枚举 busy 作为设备级防重闸；静默标志独立于 busy，
// 避免点击检测在设备快照中呈现为「正在读取」遮罩。调用方须持 a.mu。
func (a *App) beginAppListCheckLocked(key string) bool {
	if a.appListBusy == nil {
		a.appListBusy = map[string]time.Time{}
	}
	if a.appListSilent == nil {
		a.appListSilent = map[string]bool{}
	}
	if a.appListSilentChanged == nil {
		a.appListSilentChanged = map[string]bool{}
	}
	if _, busy := a.appListBusy[key]; busy {
		if _, known := a.appListSilentChanged[key]; !known {
			a.appListSilentChanged[key] = false
		}
		a.appListSilent[key] = true
		return false
	}
	a.appListBusy[key] = time.Now()
	a.appListSilent[key] = true
	a.resetAppListCheckResultLocked(key)
	return true
}

// CheckAppList 每次应用列表入口打开时发起静默差分检测。返回 watching=true 表示
// 有检测或已有枚举正在运行，前端可等它结束后读取最新缓存；设备级 busy 防止重复启动。
func (a *App) CheckAppList(serial string) (bool, error) {
	key := a.appListKeyFor(serial)
	if key == "" {
		return false, errors.New("未指定设备")
	}
	a.mu.Lock()
	start := a.beginAppListCheckLocked(key)
	a.mu.Unlock()
	if start {
		go a.runAppListEnumMode(key, serial, true)
	}
	return true, nil
}

type AppListCheckStatus struct {
	Busy    bool `json:"busy"`
	Changed bool `json:"changed"`
}

// IsAppListCheckBusy 用于前端静默等待列表及图标均处理完毕，并确认是否真的需要换列表。
func (a *App) IsAppListCheckBusy(serial string) (AppListCheckStatus, error) {
	key := a.appListKeyFor(serial)
	if key == "" {
		return AppListCheckStatus{}, errors.New("未指定设备")
	}
	a.mu.RLock()
	status := AppListCheckStatus{Busy: a.appListSilent[key], Changed: a.appListSilentChanged[key]}
	a.mu.RUnlock()
	return status, nil
}

// appListKeyFor 把"活的 serial"归一为设备身份键（identity）：优先当前设备列表，
// 回退档案 ResolveKey（覆盖刚断开/离线场景）；都找不到=以 serial 兜底。
func (a *App) appListKeyFor(serial string) string {
	if serial == "" {
		return ""
	}
	a.mu.RLock()
	for i := range a.devices {
		d := &a.devices[i]
		if d.Serial == serial || d.Wireless == serial {
			if d.Identity != "" {
				id := d.Identity
				a.mu.RUnlock()
				return id
			}
		}
	}
	a.mu.RUnlock()
	if k := a.profiles.ResolveKey(serial); k != "" {
		return k
	}
	return serial
}
