package app

import (
	"scrcpy-ez/gui/internal/adbparse"
	"scrcpy-ez/gui/internal/discovery"
)

// A renamed pairing service may differ from the connect service's instance.
// Serial and IP must agree; conflicting candidates never select a random port.
func pairingServicePort(guid, ip string, services []discovery.MdnsService) string {
	serial := adbparse.ServiceSerial(guid)
	bestRank, port, ambiguous := 0, "", false
	for _, s := range services {
		if s.Mode != discovery.MdnsModePairing || !IsIPPort(s.Addr) || !adbparse.IsIPEndpoint(s.Addr) {
			continue
		}
		if ip != "" && ipOfAddr(s.Addr) != ip {
			continue
		}
		rank := 0
		switch {
		case guid != "" && adbparse.ServiceInstance(s.Name) == adbparse.ServiceInstance(guid):
			rank = 3
		case serial != "" && adbparse.ServiceSerial(s.Name) == serial && ip != "":
			rank = 2
		case serial == "" && ip != "":
			rank = 1 // Preserve IP-only/manual and legacy unnamed discovery.
		}
		if rank == 0 || rank < bestRank {
			continue
		}
		candidate := addrPort(s.Addr)
		if rank > bestRank {
			bestRank, port, ambiguous = rank, candidate, false
		} else if candidate != port {
			ambiguous = true
		}
	}
	if ambiguous {
		return ""
	}
	return port
}
