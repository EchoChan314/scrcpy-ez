package castsupervisor

import (
	"fmt"
	"strings"

	"scrcpy-ez/gui/internal/deviceevents"
)

func transportKey(s deviceevents.Snapshot, t deviceevents.Transport) string {
	return fmt.Sprintf("%d/%s/%d", s.Epoch, t.Serial, t.Generation)
}

func current(s deviceevents.Snapshot, key string) bool {
	if !s.Available {
		return false
	}
	for _, t := range s.Transports {
		if t.State == "device" && transportKey(s, t) == key {
			return true
		}
	}
	return false
}

// Initial startup must finish resolving a possible USB route before launching
// wireless. An established wireless client can keep running during learning.
func startupWaitsForUSB(s deviceevents.Snapshot, target, lockedUSB string, identities map[string]string, blocked map[string]bool, identityFailures map[string]int) bool {
	for _, serial := range s.Learning {
		if serial == target || serial == lockedUSB {
			return true
		}
	}
	for _, t := range s.Transports {
		if t.Kind != "usb" || t.State != "device" {
			continue
		}
		if lockedUSB != "" && !strings.Contains(lockedUSB, ":") && t.Serial != lockedUSB && t.Serial != target {
			continue
		}
		key := transportKey(s, t)
		if blocked[key] || identityFailures[key] >= 3 {
			continue
		}
		id := identities[key]
		if id == "" || target == "" || id == target {
			return true
		}
	}
	return false
}

// choose uses verified full serials, never market names, model names, or addresses.
// An unavailable observer cannot authorize starting or switching a client.
func choose(s deviceevents.Snapshot, target string, identities map[string]string, blocked map[string]bool) deviceevents.Transport {
	if !s.Available {
		return deviceevents.Transport{}
	}
	held := map[string]bool{}
	for _, serial := range s.Learning {
		held[serial] = true
	}
	var usb, wifi []deviceevents.Transport
	distinct := map[string]bool{}
	for _, t := range s.Transports {
		key := transportKey(s, t)
		id := identities[key]
		if target == "" && t.State == "device" && (t.Kind == "usb" || t.Kind == "wifi") && id == "" {
			return deviceevents.Transport{}
		}
		if t.State != "device" || id == "" || (target != "" && target != id) || blocked[key] {
			continue
		}
		distinct[id] = true
		if t.Kind == "usb" && !held[t.Serial] {
			usb = append(usb, t)
		}
		if t.Kind == "wifi" {
			wifi = append(wifi, t)
		}
	}
	if target == "" && len(distinct) != 1 {
		return deviceevents.Transport{}
	}
	if len(usb) == 1 {
		return usb[0]
	}
	if len(usb) > 1 {
		return deviceevents.Transport{}
	}
	if len(wifi) > 0 {
		return wifi[0]
	}
	return deviceevents.Transport{}
}
