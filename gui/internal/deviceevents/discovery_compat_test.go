package deviceevents

import "testing"

func TestRenamedWirelessNeverEmitsUSB(t *testing.T) {
	for _, name := range []string{"adb-PHONE-Ab12Cd (2)", `adb-PHONE-Ab12Cd\ \(2\)`} {
		token := name + "._adb-tls-connect._tcp"
		for _, state := range []string{"device", "offline", "unauthorized"} {
			got := Parse(token + "\t" + state + " model:PHONE transport_id:9\nUSB\tunauthorized transport_id:10\n")
			if len(got) != 2 || got[0].Serial != token || got[0].Kind != "wifi" || got[0].State != state || got[0].ID != "9" || got[1].Kind != "usb" {
				t.Fatalf("incorrect independent transports: %+v", got)
			}
		}
	}
	if got := Parse("USB unexpected transport_id:1\n"); len(got) != 0 {
		t.Fatalf("malformed state created insertion event: %+v", got)
	}
}
