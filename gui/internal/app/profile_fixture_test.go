package app

// Historical tests describe devices by their display names. Resolve those fixture
// labels to the actual key so their address/parameter assertions keep testing the
// same behavior after new archives stop using product names as identities.
// This helper is only for single-device fixtures; ambiguous names fail to resolve.
func fixtureArchiveKey(s *ProfileStore, label string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fixtureArchiveKeyLocked(s, label)
}

func fixtureArchiveKeyLocked(s *ProfileStore, label string) string {
	if s.data.Devices[label] != nil {
		return label
	}
	if e, ok := s.resolveCurrentLocked(label); ok {
		return s.keyOfLocked(e)
	}
	found := ""
	for key, e := range s.data.Devices {
		if e.Marketname != label && e.Manufacturer+" "+e.Model != label {
			continue
		}
		if found != "" {
			return label
		}
		found = key
	}
	if found != "" {
		return found
	}
	return label
}

func fixtureEntriesKey(entries map[string]DeviceEntry, label string) string {
	if _, ok := entries[label]; ok {
		return label
	}
	found := ""
	for key, e := range entries {
		if e.Marketname != label && e.Manufacturer+" "+e.Model != label && !contains(e.Serials, label) {
			continue
		}
		if found != "" {
			return label
		}
		found = key
	}
	if found != "" {
		return found
	}
	return label
}

// A saved address-only fixture represents an installation made by an older
// version, not a new unconfirmed connection silently persisted by this version.
func saveLegacyFixture(s *ProfileStore, key string, p DeviceProfile) error {
	s.mu.Lock()
	if _, ok := s.resolveLocked(key); !ok && IsIPPort(key) {
		s.data.Devices[key] = entryFromKey(key, p)
	}
	s.mu.Unlock()
	return s.Save(key, p)
}

func fixtureCandidateKeys(s *ProfileStore, cands map[string][]AddrEntry) map[string][]AddrEntry {
	resolved := make(map[string][]AddrEntry, len(cands))
	for key, entries := range cands {
		resolved[fixtureArchiveKey(s, key)] = entries
	}
	return resolved
}
