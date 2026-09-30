package app

import (
	"fmt"
	"time"
)

// 当前 mDNS 地址仍 active 时，旧 transport 快照不能替换它。
func (s *ProfileStore) snapshotAddressAllowedLocked(e *DeviceEntry, addr string) bool {
	class := ModeTcpip
	if isTlsFormAddr(addr) {
		class = ModeTls
	}
	current := s.mdnsAuthority[e][class]
	if current == "" || current == addr {
		return true
	}
	for _, candidate := range e.Addrs {
		if candidate.Addr == current && candidate.State == AddrStateActive {
			return false
		}
	}
	return true
}

func (s *ProfileStore) addressEvidenceCurrent(key, addr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	return ok && s.snapshotAddressAllowedLocked(e, addr)
}

func (s *ProfileStore) addrSuccessIfCurrent(key, addr, mode string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok || !s.snapshotAddressAllowedLocked(e, addr) {
		return false
	}
	changed := s.addrSuccessModeLocked(e, addr, mode, time.Now())
	changed = s.applyWirelessFormLocked(e, mode) || changed
	if changed {
		_ = s.persistLocked()
	}
	return true
}

// 学习成功必须包含落盘成功；已有同样地址也重试尚未完成的落盘。
func (s *ProfileStore) LearnWirelessAddr(key, addr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok {
		return fmt.Errorf("设备档案尚未就绪：%s", key)
	}
	s.addrSuccessModeLocked(e, addr, ModeTcpip, time.Now())
	s.applyWirelessFormLocked(e, ModeTcpip)
	s.rememberMdnsAddressesLocked([]MdnsAddr{{Addr: addr, Mode: ModeTcpip}})
	return s.persistLocked()
}

func (s *ProfileStore) rememberMdnsAddressesLocked(matches []MdnsAddr) {
	if s.mdnsAuthority == nil {
		s.mdnsAuthority = map[*DeviceEntry]map[string]string{}
	}
	for _, match := range matches {
		e, ok := s.resolveLocked(match.Addr)
		if !ok {
			continue
		}
		if s.mdnsAuthority[e] == nil {
			s.mdnsAuthority[e] = map[string]string{}
		}
		s.mdnsAuthority[e][match.Mode] = match.Addr
	}
}
