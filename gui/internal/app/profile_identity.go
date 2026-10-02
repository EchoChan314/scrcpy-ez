package app

import (
	"strings"

	"scrcpy-ez/gui/internal/adb"
)

func deviceShortSerial(d *adb.Device) string {
	if d == nil {
		return ""
	}
	if serial := adb.StableSerial(d.StableSerial); serial != "" {
		return serial
	}
	return adb.StableSerial(d.Serial)
}

func serialCompatible(e *DeviceEntry, serial string) bool {
	return serial == "" || len(e.Serials) == 0 || contains(e.Serials, serial)
}

// An empty side is not evidence that two archives represent the same phone.
func sameArchiveIdentity(a, b *DeviceEntry) bool {
	for _, serial := range a.Serials {
		if serial != "" && contains(b.Serials, serial) {
			return true
		}
	}
	if len(a.Serials) > 0 && len(b.Serials) > 0 {
		return false
	}
	return a.TlsGuid != "" && a.TlsGuid == b.TlsGuid
}

func (s *ProfileStore) hasSerialClaimLocked(serial string) bool {
	for key, e := range s.data.Devices {
		if key == "device:"+serial || key == serial || contains(e.Serials, serial) {
			return true
		}
	}
	return false
}

// One scan per device snapshot avoids repeated full archive scans. These indexes
// are local to the locked transaction, so learning/deletion cannot leave stale
// identity caches behind.
type snapshotIdentityIndex struct {
	serials   map[string]*DeviceEntry
	claims    map[string]bool
	keys      map[*DeviceEntry]string
	addresses map[string]*DeviceEntry
}

func buildSnapshotIdentityIndex(devices map[string]*DeviceEntry) snapshotIdentityIndex {
	idx := snapshotIdentityIndex{serials: make(map[string]*DeviceEntry, len(devices)), claims: make(map[string]bool, len(devices)), keys: make(map[*DeviceEntry]string, len(devices)), addresses: make(map[string]*DeviceEntry, len(devices)*2)}
	add := func(serial string, e *DeviceEntry) {
		if serial == "" {
			return
		}
		if old, ok := idx.serials[serial]; ok && old != e {
			idx.serials[serial] = nil
		} else if !ok {
			idx.serials[serial] = e
		}
		idx.claims[serial] = true
	}
	for key, e := range devices {
		idx.keys[e] = key
		for _, serial := range e.Serials {
			add(serial, e)
		}
		if strings.HasPrefix(key, "device:") {
			serial := strings.TrimPrefix(key, "device:")
			if serialCompatible(e, serial) {
				add(serial, e)
			} else {
				idx.claims[serial] = true
				idx.serials[serial] = nil
			}
		} else if serialCompatible(e, key) {
			add(key, e)
		}
		for _, addr := range e.Addrs {
			if old, ok := idx.addresses[addr.Addr]; ok && old != e {
				idx.addresses[addr.Addr] = nil
			} else if !ok {
				idx.addresses[addr.Addr] = e
			}
		}
	}
	return idx
}

func refreshUnchangedAddress(e *DeviceEntry, addr string, now int64) bool {
	for i := range e.Addrs {
		a := &e.Addrs[i]
		if a.Addr == addr && a.State == AddrStateActive && a.Fail == 0 && a.LastFail == 0 && !a.Stale && a.Mode != "" {
			if a.Mode == ModeTls && e.Wireless != ModeTls || a.Mode == ModeTcpip && e.Wireless == "" {
				return false
			}
			a.LastOk = now
			return true
		}
	}
	return false
}

func (s *ProfileStore) resolveSerialLocked(serial string) *DeviceEntry {
	if serial == "" {
		return nil
	}
	var found *DeviceEntry
	for key, e := range s.data.Devices {
		if !contains(e.Serials, serial) && key != "device:"+serial && !(key == serial && serialCompatible(e, serial)) {
			continue
		}
		if !serialCompatible(e, serial) || found != nil && found != e {
			return nil // Ambiguous historical data must not choose a random map entry.
		}
		found = e
	}
	return found
}

// confirmedTransport is used by ADB grouping. Reading existing associations does
// not issue device queries or change the archive.
func (s *ProfileStore) confirmedTransport(transport string) (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stable := adb.StableSerial(transport)
	var e *DeviceEntry
	if stable != "" {
		e = s.resolveSerialLocked(stable)
	} else {
		e, _ = s.resolveCurrentLocked(transport)
		if e != nil {
			class := ModeTcpip
			if isTlsFormAddr(transport) {
				class = ModeTls
			}
			if s.mdnsAuthority[e][class] != transport {
				return "", ""
			}
		}
	}
	if e == nil || len(e.Serials) == 0 {
		return "", ""
	}
	if stable == "" && len(e.Serials) == 1 {
		stable = e.Serials[0]
	}
	return s.keyOfLocked(e), stable
}

// resolveDeviceLocked gives strong serial evidence precedence over addresses.
// Missing evidence preserves an existing connection but never uses product names.
func (s *ProfileStore) resolveDeviceLocked(d *adb.Device) *DeviceEntry {
	serial := deviceShortSerial(d)
	if serial != "" {
		return s.resolveSerialLocked(serial)
	}
	if e := s.data.Devices[d.Identity]; e != nil && !strings.HasPrefix(d.Identity, "pending:") {
		return e
	}
	for _, transport := range []string{d.Serial, d.Wireless} {
		if transport == "" {
			continue
		}
		if e, ok := s.resolveCurrentLocked(transport); ok {
			return e
		}
	}
	// A single active same-IP owner preserves the old-port display path. This
	// lookup does not authorize learning a serial or moving its stored address.
	if IsIPPort(d.Serial) {
		if key := s.uniqueActiveIPLocked(ipOfAddr(d.Serial)); key != "" {
			return s.data.Devices[key]
		}
	}
	return nil
}

func (s *ProfileStore) uniqueActiveIPLocked(ip string) string {
	key := ""
	for candidate, e := range s.data.Devices {
		for _, addr := range e.Addrs {
			if ipOfAddr(addr.Addr) != ip || addr.State != AddrStateActive {
				continue
			}
			if key != "" && key != candidate {
				return ""
			}
			key = candidate
			break
		}
	}
	return key
}

func (s *ProfileStore) DeviceKey(d *adb.Device) string {
	if d == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.resolveDeviceLocked(d); e != nil {
		return s.keyOfLocked(e)
	}
	return ""
}

// claimAddressLocked retires another owner's exact address after a confirmed
// identity reports there. Historical session aliases stay with their old owner.
func (s *ProfileStore) claimAddressLocked(owner *DeviceEntry, addr string) bool {
	changed := false
	for _, e := range s.data.Devices {
		if e == owner {
			continue
		}
		kept := e.Addrs[:0]
		for _, item := range e.Addrs {
			if item.Addr == addr {
				changed = true
				if s.addressAliases == nil {
					s.addressAliases = map[string]*DeviceEntry{}
				}
				s.addressAliases[addr] = e
				continue
			}
			kept = append(kept, item)
		}
		e.Addrs = kept
	}
	return changed
}

func (a *App) annotateDeviceIdentities(devs []adb.Device) {
	services := a.mdnsSnapshot()
	for i := range devs {
		d := &devs[i]
		if d.ConnType == "wifi" {
			d.IdentityEpoch = a.wirelessEpoch(d.Serial)
			if serial := a.verifiedWirelessSerial(d.Serial); serial != "" {
				d.StableSerial = serial
			} else if serial, _ := serialFromMdnsServices(services, d.Serial); serial != "" {
				d.StableSerial = serial
			}
		}
		if key := a.profiles.DeviceKey(d); key != "" {
			d.Identity = key
		} else {
			serial := deviceShortSerial(d)
			if serial == "" {
				serial = d.Serial
			}
			d.Identity = adb.IdentityKey("", "", "", serial)
		}
	}
}

// Detection must not hide a new same-model phone before the application sees it.
// A current broadcast supplies the WiFi identity; remembered IP ownership alone
// is insufficient to merge a bare network transport into a USB group.
func (a *App) confirmedADBIdentity(transport string) (string, string) {
	if IsIPPort(transport) {
		serial := a.verifiedWirelessSerial(transport)
		if serial == "" {
			serial, _ = serialFromMdnsServices(a.mdnsSnapshot(), transport)
		}
		if serial == "" {
			return "", ""
		}
		key := a.profiles.ResolveKey(serial)
		if key == "" {
			key = adb.IdentityKey("", "", "", serial)
		}
		return key, serial
	}
	return a.profiles.confirmedTransport(transport)
}

func (s *ProfileStore) SaveError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.persistError != nil {
		return s.persistError.Error()
	}
	return ""
}

// A running cast retains its archive when its old transport address is reused.
func (a *App) profileKeyForSession(serial string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if st := a.sessions[serial]; st != nil && st.identity != "" && (st.runner != nil || st.restarting) {
		return st.identity
	}
	return serial
}

// Window controls refer to the original session even if its IP was reassigned.
func (a *App) appWinProfileKey(serial, pkg string) string {
	a.mu.RLock()
	st := a.appWins[appWinKey(serial, pkg)]
	identity := ""
	if st != nil {
		identity = st.identity
	}
	a.mu.RUnlock()
	if identity != "" {
		return identity
	}
	return a.appListKeyFor(serial)
}

// Address lookup is only a candidate; serial evidence must agree before use.
func (s *ProfileStore) MdnsKey(serial, addr, guid string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.resolveSerialLocked(serial); e != nil {
		return s.keyOfLocked(e)
	}
	if e := s.resolveGuidLocked(guid); e != nil && serialCompatible(e, serial) {
		return s.keyOfLocked(e)
	}
	if e, ok := s.resolveCurrentLocked(addr); ok && serialCompatible(e, serial) {
		return s.keyOfLocked(e)
	}
	if serial != "" {
		if e := s.claimableByIPLocked(ipOfAddr(addr), serial); e != nil {
			return s.keyOfLocked(e)
		}
	}
	return ""
}

func (s *ProfileStore) resolveGuidLocked(guid string) *DeviceEntry {
	if guid == "" {
		return nil
	}
	var found *DeviceEntry
	for _, e := range s.data.Devices {
		if e.TlsGuid != guid {
			continue
		}
		if found != nil && found != e {
			return nil
		}
		found = e
	}
	return found
}
