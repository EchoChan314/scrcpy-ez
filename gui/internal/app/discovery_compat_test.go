package app

import (
	"context"
	"testing"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/deviceevents"
	"scrcpy-ez/gui/internal/discovery"
)

func TestRenamedKnownDeviceDoesNotBecomePending(t *testing.T) {
	a, _ := newTestApp()
	t.Cleanup(a.Close)
	gui31K80Profiles(a)
	for _, name := range []string{"adb-TEST0001-KWqpio (2)", `adb-TEST0001-KWqpio\ \(2\)`, `adb-TEST0001-KWqpio\032\0402\041`} {
		for _, identity := range []string{TlsServiceIdentity(name), SerialFromServiceName(name), adb.StableSerial(name + "._adb-tls-connect._tcp.local.")} {
			if identity != "TEST0001" {
				t.Fatalf("identity paths disagree for %q: %q", name, identity)
			}
		}
		svc := mdnsTlsSvc(name, "192.0.2.197", "45006")
		a.mdns = []discovery.MdnsService{svc}
		a.buildPending(nil)
		if pending := a.Snapshot().Pending; len(pending) != 0 {
			t.Fatalf("known renamed phone offered for new pairing: %+v", pending)
		}
		if got := a.profiles.MdnsKey(SerialFromServiceName(svc.Name), svc.Addr, svc.Name); got != a.profiles.ResolveKey("TEST0001") {
			t.Fatalf("renamed phone lost existing archive: %q", got)
		}
	}
	// A genuinely different device at the old IP must remain independent.
	a.mdns = []discovery.MdnsService{mdnsTlsSvc("adb-OTHER-KWqpio (2)", "192.0.2.197", "45006")}
	a.buildPending(nil)
	if pending := a.Snapshot().Pending; len(pending) != 1 || pending[0].Serial != "OTHER" {
		t.Fatalf("DHCP reuse was merged with old phone: %+v", pending)
	}
	if got := SerialFromServiceName(`adb-TEST\\032._adb-tls-connect._tcp.local.`); got != "" {
		t.Fatalf("escaped backslash decoded twice into identity %q", got)
	}
}

func TestRenamedWirelessEventsDoNotStartUSBShield(t *testing.T) {
	a, _ := newTestApp()
	t.Cleanup(a.Close)
	gui31K80Profiles(a)
	for _, name := range []string{"adb-TEST0001-KWqpio (2)", `adb-TEST0001-KWqpio\ \(2\)`} {
		block := name + "._adb-tls-connect._tcp\toffline transport_id:9\n"
		raw := snapshotFor(a, deviceevents.Parse(block)...)
		a.applyDeviceSnapshot(context.Background(), adb.ParseTrackDevices(block), &raw, "compatibility-test")
		if len(a.lastUSB) != 0 || a.plugActiveSerial("TEST0001") {
			t.Fatalf("wireless event started USB insertion: %+v", a.lastUSB)
		}
		for _, d := range a.Snapshot().Devices {
			if d.ConnType == "usb" && d.Connecting {
				t.Fatalf("wireless event synthesized USB shield: %+v", d)
			}
		}
	}
}

func TestPairingServicePortIdentityAndAmbiguity(t *testing.T) {
	const ip = "192.0.2.9"
	const guid = "adb-PHONE-Ab12Cd"
	svc := func(name, addr string) discovery.MdnsService {
		return discovery.MdnsService{Name: name, Addr: addr, Mode: discovery.MdnsModePairing}
	}
	cases := []struct {
		name, guid, ip, want string
		services             []discovery.MdnsService
	}{
		{"normal", guid, ip, "37001", []discovery.MdnsService{svc(guid, ip+":37001")}},
		{"renamed", guid, ip, "37001", []discovery.MdnsService{svc(`adb-PHONE-Ab12Cd\ \(2\)`, ip+":37001")}},
		{"other serial first", guid, ip, "37001", []discovery.MdnsService{svc("adb-OTHER-Ab12Cd", ip+":37002"), svc(guid+" (2)", ip+":37001")}},
		{"wrong IP", guid, ip, "", []discovery.MdnsService{svc(guid, "192.0.2.8:37001")}},
		{"wrong serial", guid, ip, "", []discovery.MdnsService{svc("adb-OTHER-Ab12Cd", ip+":37001")}},
		{"ambiguous renamed", guid, ip, "", []discovery.MdnsService{svc(guid+" (2)", ip+":37001"), svc(guid+" (3)", ip+":37002")}},
		{"exact before renamed", guid, ip, "37001", []discovery.MdnsService{svc(guid+" (2)", ip+":37002"), svc(guid, ip+":37001")}},
		{"manual IP", "", ip, "37001", []discovery.MdnsService{svc("studio-custom", ip+":37001")}},
		{"manual ambiguous", "", ip, "", []discovery.MdnsService{svc("studio-a", ip+":37001"), svc("studio-b", ip+":37002")}},
		{"no selector", "", "", "", []discovery.MdnsService{svc(guid, ip+":37001")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pairingServicePort(tc.guid, tc.ip, tc.services); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
	a, _ := newTestApp()
	t.Cleanup(a.Close)
	a.mdns = []discovery.MdnsService{mdnsTlsSvc(guid+" (2)", ip, "45001"), svc(guid, ip+":37001")}
	a.buildPending(nil)
	if pending := a.Snapshot().Pending; len(pending) != 1 || pending[0].PairPort != "37001" || pending[0].Name != "PHONE" {
		t.Fatalf("pending renamed service lost pairing port/display name: %+v", pending)
	}
}
