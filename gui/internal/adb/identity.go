package adb

import "strings"

// StableSerial uses the complete USB serial or the serial in an ADB service name.
// An address, pending key or placeholder is never a permanent device identity.
func StableSerial(transport string) string {
	s := strings.TrimSpace(transport)
	if s == "" || strings.EqualFold(s, "unknown") || strings.EqualFold(s, "null") || strings.Contains(s, ":") {
		return ""
	}
	if strings.HasPrefix(s, "adb-") {
		for _, suffix := range []string{"._adb-tls-connect._tcp", "._adb._tcp", "._adb-tls-connect", "._adb", "._tcp"} {
			s = strings.TrimSuffix(s, suffix)
		}
		s = strings.TrimPrefix(s, "adb-")
		if i := strings.LastIndexByte(s, '-'); i > 0 && len(s)-i-1 == 6 {
			valid := true
			for _, ch := range s[i+1:] {
				if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z') {
					valid = false
				}
			}
			if valid {
				s = s[:i]
			}
		}
	}
	return s
}

// IdentityKey is a provisional identity before the profile store supplies its
// immutable key. Product names are display metadata, never identity evidence.
func IdentityKey(marketname, manufacturer, model, serial string) string {
	if stable := StableSerial(serial); stable != "" {
		return "device:" + stable
	}
	if strings.TrimSpace(serial) != "" {
		return "pending:" + strings.TrimSpace(serial)
	}
	return ""
}

// SetIdentityResolver lets detection reuse confirmed in-memory associations.
// It does not perform ADB queries. Set it before starting detection.
func (m *Manager) SetIdentityResolver(resolve func(string) (string, string)) {
	m.identityResolver = resolve
}
