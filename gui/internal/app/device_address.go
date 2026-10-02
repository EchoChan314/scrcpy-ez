package app

import (
	"strings"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
)

// 所有投屏入口共用：旧会话键只用于认设备，实际地址从当前档案一次性读取。
func (a *App) deviceLockParams(serial string, devices []adb.Device) bridge.CastParams {
	var params bridge.CastParams
	entry, known := a.profiles.Entry(serial)
	target := findTargetDevice(serial, devices)
	if target == nil && known {
		target = findTargetByIdentity(entry, devices)
	}
	key := serial
	if target != nil {
		id := a.identityOf(target)
		if id != "" {
			key = id
		}
		if target.ConnType == "usb" || (target.ConnType == "" && !strings.Contains(target.Serial, ":")) {
			params.Serial = target.Serial
		} else {
			params.Serial = a.usbSerialByIdentity(id, target.Serial, devices)
		}
	}
	entry, main, alternate, _ := a.profiles.connectionInfo(key)
	if target != nil {
		params.ExpectedSerial = deviceShortSerial(target)
	}
	if params.ExpectedSerial == "" && len(entry.Serials) == 1 {
		params.ExpectedSerial = entry.Serials[0]
	}
	params.Addr, params.Addr2 = main, alternate
	if params.Addr == "" && target != nil && target.ConnType == "wifi" {
		params.Addr = target.Serial
	}
	if params.Serial == "" && params.Addr == "" && !strings.HasPrefix(serial, "device:") && !strings.HasPrefix(serial, "pending:") {
		if IsIPPort(serial) {
			params.Addr = serial
		} else {
			params.Serial = serial
			params.ExpectedSerial = adb.StableSerial(serial)
		}
	}
	if params.Serial != "" || params.Addr != "" {
		params.Market, params.Model = entry.Marketname, entry.Model
	}
	return params
}

func (s *ProfileStore) connectionInfo(key string) (DeviceEntry, string, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok {
		return DeviceEntry{}, "", "", false
	}
	candidates := s.orderedAddrsLocked(e)
	main, alternate := "", ""
	if len(candidates) > 0 {
		main = candidates[0].Addr
		class := addrEntryClass(candidates[0])
		for _, candidate := range candidates[1:] {
			if candidate.State == AddrStateActive && addrEntryClass(candidate) != class {
				alternate = candidate.Addr
				break
			}
		}
	}
	return cloneEntry(e), main, alternate, true
}
