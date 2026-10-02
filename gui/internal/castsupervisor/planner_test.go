package castsupervisor

import (
	"scrcpy-ez/gui/internal/deviceevents"
	"testing"
)

func TestSameDevicePolicy(t *testing.T) {
	a := deviceevents.Transport{Serial: "USB_A", Kind: "usb", State: "device", Generation: 1}
	b := deviceevents.Transport{Serial: "USB_B", Kind: "usb", State: "device", Generation: 1}
	w := deviceevents.Transport{Serial: "192.0.2.1:5555", Kind: "wifi", State: "device", Generation: 1}
	cases := []struct {
		name       string
		transports []deviceevents.Transport
		ids        []string
		target     string
		held       []string
		available  bool
		want       string
	}{
		{"USB exact", []deviceevents.Transport{a}, []string{"PHONE_A"}, "PHONE_A", nil, true, "USB_A"},
		{"same model other USB", []deviceevents.Transport{b, w}, []string{"PHONE_B", "PHONE_A"}, "PHONE_A", nil, true, w.Serial},
		{"DHCP reused by another phone", []deviceevents.Transport{w}, []string{"PHONE_B"}, "PHONE_A", nil, true, ""},
		{"missing wireless identity", []deviceevents.Transport{w}, []string{""}, "PHONE_A", nil, true, ""},
		{"USB preferred", []deviceevents.Transport{w, a}, []string{"PHONE_A", "PHONE_A"}, "PHONE_A", nil, true, "USB_A"},
		{"learning keeps wifi", []deviceevents.Transport{w, a}, []string{"PHONE_A", "PHONE_A"}, "PHONE_A", []string{"USB_A"}, true, w.Serial},
		{"stream lost", []deviceevents.Transport{a}, []string{"PHONE_A"}, "PHONE_A", nil, false, ""},
		{"free multiple phones", []deviceevents.Transport{a, b}, []string{"PHONE_A", "PHONE_B"}, "", nil, true, ""},
		{"free one phone two transports", []deviceevents.Transport{w, a}, []string{"PHONE_A", "PHONE_A"}, "", nil, true, "USB_A"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := deviceevents.Snapshot{Epoch: 1, Available: c.available, Transports: c.transports, Learning: c.held}
			ids := map[string]string{}
			for i, v := range c.transports {
				ids[transportKey(s, v)] = c.ids[i]
			}
			if got := choose(s, c.target, ids, nil); got.Serial != c.want {
				t.Fatalf("got %+v want %q", got, c.want)
			}
		})
	}
	for _, state := range []string{"unauthorized", "offline", "recovery", "bootloader"} {
		a.State = state
		s := deviceevents.Snapshot{Epoch: 1, Available: true, Transports: []deviceevents.Transport{a}}
		if got := choose(s, "PHONE_A", map[string]string{transportKey(s, a): "PHONE_A"}, nil); got.Serial != "" {
			t.Fatalf("started %s", state)
		}
	}
}

func TestLateIdentityAndFailureBudget(t *testing.T) {
	a := deviceevents.Transport{Serial: "A", Kind: "usb", State: "device", Generation: 1}
	s := deviceevents.Snapshot{Epoch: 1, Available: true, Transports: []deviceevents.Transport{a}}
	key := transportKey(s, a)
	s.Transports[0].Generation = 2
	if current(s, key) {
		t.Fatal("old identity result accepted after reinsert")
	}
	newkey := transportKey(s, s.Transports[0])
	ids := map[string]string{key: "PHONE_A", newkey: "PHONE_A"}
	if got := choose(s, "PHONE_A", ids, map[string]bool{newkey: true}); got.Serial != "" {
		t.Fatal("exhausted generation restarted")
	}
	s.Epoch = 2
	if current(s, newkey) {
		t.Fatal("old observer accepted")
	}
}
