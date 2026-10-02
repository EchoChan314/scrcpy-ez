package adb

import (
	"context"
	"testing"
	"time"
)

func TestIdentityCacheAddressReuseDoesNotReusePreviousDeviceSpecs(t *testing.T) {
	addr := "192.0.2.10:5555"
	m := New("adb-unavailable-for-identity-test", "")
	now := time.Now()
	m.cache[addr] = &cachedSpec{identity: "device:A", name: "phone A", marketname: "phone A", model: "model A", res: "3200x1440", fps: 120, battery: 90, at: now, specAt: now, batAt: now}
	owner := ""
	m.SetIdentityResolver(func(string) (string, string) { return owner, "" })
	d := Device{Serial: addr}
	m.enrich(context.Background(), &d)
	if d.Name != "phone A" || d.Res != "3200x1440" {
		t.Fatal("a missing identity observation invalidated confirmed cached data", d)
	}
	owner = "device:B"
	m.enrich(context.Background(), &d)
	if d.Name == "phone A" || d.Model == "model A" || d.Res != "" || d.Battery != 0 {
		t.Fatal("new owner inherited previous device metadata", d)
	}
}
