package adb

import (
	"strings"

	"scrcpy-ez/gui/internal/adbparse"
)

// StableSerial uses the complete USB serial or the serial in an ADB service name.
// An address, pending key or placeholder is never a permanent device identity.
func StableSerial(transport string) string {
	s := strings.TrimSpace(transport)
	if s == "" || strings.EqualFold(s, "unknown") || strings.EqualFold(s, "null") || strings.Contains(s, ":") {
		return ""
	}
	if strings.HasPrefix(s, "adb-") {
		return adbparse.ServiceSerial(s)
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
