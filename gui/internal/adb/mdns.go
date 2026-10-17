package adb

import (
	"regexp"
	"strings"

	"scrcpy-ez/gui/internal/adbparse"
)

// mDNS 服务形态（与档案 wireless/mode 字段同一语义）：
// tls = 无线调试加密连接（_adb-tls-connect._tcp）；tcpip = 经典明文 5555（_adb._tcp）；
// pairing = 配对服务（_adb-tls-pairing._tcp，仅配对弹窗打开期间短暂广播）。
const (
	MdnsModeTls     = "tls"
	MdnsModeTcpip   = "tcpip"
	MdnsModePairing = "pairing"
)

// MdnsService 是一条 mDNS 服务解析结果。
// `adb mdns services` 有两代输出格式（见 ParseMdnsServices 列序判定）：
// 老 adb：<service-type> \t <instance-name> \t <resolved-addr>；
// adb 37.0.0：<instance-name> \t <service-type> \t <resolved-addr>。
// 地址列可能是 IP:port（已解析）或 "local"（未解析，无地址）。
// 自管 zeroconf 浏览（mdns_zeroconf.go）也映射为本结构。
type MdnsService struct {
	Type string `json:"type"` // _adb-tls-connect._tcp 等
	Name string `json:"name"` // 服务实例名（tls 形如 adb-<serial>-XXXXXX）
	Addr string `json:"addr"` // 解析出的 IP:port；未解析为空
	Mode string `json:"mode"` // tls / tcpip / pairing（按服务类型判定）
}

var (
	reHeader             = regexp.MustCompile(`(?i)^list of`)
	reIPPort             = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}:\d+$`)
	reMdnsTypeColumn     = regexp.MustCompile(`(?i)(?:^|[ \t]+)(_adb[-_a-z0-9]*\._tcp(?:\.local)?\.?)(?:[ \t]+|$)`)
	reMdnsCollisionLabel = regexp.MustCompile(`^\([1-9][0-9]*\)$`)
)

// MdnsModeOf 按服务类型判定形态：tls 连接/配对服务归 tls/pairing，其余 _adb 服务归 tcpip。
func MdnsModeOf(svcType string) string {
	switch {
	case strings.Contains(svcType, "-tls-connect"):
		return MdnsModeTls
	case strings.Contains(svcType, "-tls-pairing"):
		return MdnsModePairing
	default:
		return MdnsModeTcpip
	}
}

// ParseMdnsServices 解析 `adb mdns services` 输出。
// Prefer tab-delimited columns, then locate the service-type boundary in older
// space-aligned output. Preserve instance names including spaces and DNS escapes.
// 地址列仅接受 IP:port（"local"/主机名等未解析值忽略）。
func ParseMdnsServices(output string) []MdnsService {
	var out []MdnsService
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		line = strings.TrimSpace(line)
		if line == "" || reHeader.MatchString(line) {
			continue
		}
		svcType, name, addr, ok := mdnsColumns(line)
		if !ok {
			continue
		}
		s := MdnsService{Type: svcType, Name: name, Mode: MdnsModeOf(svcType)}
		addr = strings.TrimSuffix(addr, ".")
		if reIPPort.MatchString(addr) && adbparse.IsIPEndpoint(addr) {
			s.Addr = addr
		}
		out = append(out, s)
	}
	return out
}

func mdnsColumns(line string) (svcType, name, addr string, ok bool) {
	columns := strings.Split(line, "\t")
	if len(columns) >= 2 {
		for i := 0; i < 2; i++ {
			if st, valid := mdnsTypeColumn(columns[i]); valid {
				name = strings.TrimSpace(columns[1-i])
				if len(columns) >= 3 {
					addr = strings.TrimSpace(columns[2])
				}
				return st, strings.TrimSuffix(name, "."), addr, name != ""
			}
		}
	}
	match := reMdnsTypeColumn.FindStringSubmatchIndex(line)
	if match == nil {
		return "", "", "", false
	}
	svcType, _ = mdnsTypeColumn(line[match[2]:match[3]])
	if match[2] != 0 {
		name = strings.TrimSpace(line[:match[2]])
		addr = strings.TrimSpace(line[match[3]:])
	} else {
		name = strings.TrimSpace(line[match[3]:])
		if last := strings.LastIndexAny(name, " \t"); last >= 0 {
			tail := strings.TrimSpace(name[last+1:])
			if !reMdnsCollisionLabel.MatchString(adbparse.ServiceInstance(tail)) {
				addr, name = tail, strings.TrimSpace(name[:last])
			}
		}
	}
	return svcType, strings.TrimSuffix(name, "."), addr, name != ""
}

func mdnsTypeColumn(column string) (string, bool) {
	s := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(column), "."))
	s = strings.TrimSuffix(s, ".local")
	return s, strings.HasPrefix(s, "_adb") && strings.HasSuffix(s, "._tcp") && !strings.ContainsAny(s, " \t")
}

// MdnsTrackEvents 是一份 mdns 全量快照（First=true 仅首个快照一次）。
// 事件处理链对快照做 diff；自管 zeroconf 浏览按此模型对外输出
// 都按此模型对外输出。
type MdnsTrackEvents struct {
	Added    []MdnsService
	Removed  []MdnsService
	Snapshot []MdnsService
	First    bool
}

// DiffMdnsServices 对两份 mdns 服务快照做 diff（纯函数）。
// key = Addr（空 Addr 用 Name）；同 key 视为同一服务。
func DiffMdnsServices(prev, cur []MdnsService) (added, removed []MdnsService) {
	prevIdx := map[string]int{}
	for i := range prev {
		prevIdx[mdnsServiceKey(prev[i])] = i
	}
	curIdx := map[string]int{}
	for i := range cur {
		curIdx[mdnsServiceKey(cur[i])] = i
	}
	for i := range cur {
		if _, ok := prevIdx[mdnsServiceKey(cur[i])]; !ok {
			added = append(added, cur[i])
		}
	}
	for i := range prev {
		if _, ok := curIdx[mdnsServiceKey(prev[i])]; !ok {
			removed = append(removed, prev[i])
		}
	}
	return added, removed
}

func mdnsServiceKey(s MdnsService) string {
	if s.Addr != "" {
		return "addr:" + s.Addr
	}
	if s.Name != "" {
		return "name:" + s.Name
	}
	return "type:" + s.Type
}
