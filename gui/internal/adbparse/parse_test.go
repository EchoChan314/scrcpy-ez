package adbparse

import (
	"strings"
	"testing"
)

func TestDeviceLineCompatibility(t *testing.T) {
	cases := []struct{ line, serial, state, kind string }{
		{"USB123\tdevice", "USB123", "device", "usb"},
		{"USB123   unauthorized transport_id:3", "USB123", "unauthorized", "usb"},
		{"USB123\toffline", "USB123", "offline", "usb"},
		{"USB123 recovery", "USB123", "recovery", "usb"},
		{"USB123 no permissions (missing udev rules)", "USB123", "no permissions", "usb"},
		{"192.0.2.10:5555 device model:PHONE", "192.0.2.10:5555", "device", "wifi"},
		{"[fe80::1%4]:5555 offline", "[fe80::1%4]:5555", "offline", "wifi"},
		{"phone.local:5555 device", "phone.local:5555", "device", "wifi"},
		{"emulator-5554 device", "emulator-5554", "device", "other"},
		{"adb-PHONE-Ab12Cd._adb-tls-connect._tcp device", "adb-PHONE-Ab12Cd._adb-tls-connect._tcp", "device", "wifi"},
		{"adb-PHONE-Ab12Cd (2)._adb-tls-connect._tcp\tdevice model:PHONE", "adb-PHONE-Ab12Cd (2)._adb-tls-connect._tcp", "device", "wifi"},
		{`adb-PHONE-Ab12Cd\ \(2\)._adb-tls-connect._tcp offline`, `adb-PHONE-Ab12Cd\ \(2\)._adb-tls-connect._tcp`, "offline", "wifi"},
		{"adb-PHONE (12)._adb._tcp.local. unauthorized", "adb-PHONE (12)._adb._tcp.local.", "unauthorized", "wifi"},
		{"adb-PHONE (2)._adb-tls-pairing._tcp device", "adb-PHONE (2)._adb-tls-pairing._tcp", "device", "other"},
		{"adb-custom device name (2)._adb-tls-connect._tcp\tdevice", "adb-custom device name (2)._adb-tls-connect._tcp", "device", "wifi"},
		{"adb-custom device name (2)._adb-tls-connect._tcp   device", "adb-custom device name (2)._adb-tls-connect._tcp", "device", "wifi"},
		{"device device", "device", "device", "usb"},
		{"A-IP:device device usb:2", "A-IP:device", "device", "usb"},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			d, ok := ParseDeviceLine(tc.line)
			if !ok || d.Serial != tc.serial || d.State != tc.state || TransportKind(d.Serial, d.Attributes) != tc.kind {
				t.Fatalf("parsed %+v (%v), want serial=%q state=%q kind=%q", d, ok, tc.serial, tc.state, tc.kind)
			}
		})
	}
	for _, line := range []string{"", "List of devices attached", "garbage", "PHONE unexpected transport_id:1", "PHONE\tbroken", "PHONE device\x00 model:PHONE", "PHONE device\nOTHER device", "not a transport device"} {
		if d, ok := ParseDeviceLine(line); ok {
			t.Fatalf("accepted malformed line %q as %+v", line, d)
		}
	}
}

func TestServiceIdentityCompatibility(t *testing.T) {
	for _, input := range []string{
		"adb-PHONE-Ab12Cd", "adb-PHONE-Ab12Cd (2)", "adb-PHONE-Ab12Cd (12) (3)",
		`adb-PHONE-Ab12Cd\ \(2\)`, `adb-PHONE-Ab12Cd\032\0402\041`,
		"adb-PHONE-Ab12Cd (2)._adb-tls-connect._tcp.local.",
		`adb-PHONE-Ab12Cd\ \(2\)._adb-tls-pairing._tcp.local.`,
		"adb-PHONE-Ab12Cd (2)._ADB-TLS-CONNECT._TCP.LOCAL.",
	} {
		if got := ServiceSerial(input); got != "PHONE" {
			t.Fatalf("ServiceSerial(%q) = %q", input, got)
		}
	}
	for _, input := range []string{"studio-PHONE-Ab12Cd (2)", "adb-PHONE-Ab12Cd (unknown)", `adb-PHONE-Ab12Cd\`, `adb-PHONE\999`, `adb-PHONE\000`, `adb-PHONE\001`, `adb-PHONE\7`} {
		if got := ServiceSerial(input); got != "" {
			t.Fatalf("untrusted name %q became serial %q", input, got)
		}
	}
	if got := ServiceSerial("adb-PHONE-WITH-HYPHENS-Ab12Cd (2)"); got != "PHONE-WITH-HYPHENS" {
		t.Fatal(got)
	}
	if got := ServiceInstance(`adb-PHONE\\032._adb._tcp.local.`); got != `adb-PHONE\032` {
		t.Fatalf("DNS escapes decoded more than once: %q", got)
	}
}

func FuzzDeviceLine(f *testing.F) {
	for _, line := range []string{"USB\tdevice", "adb-PHONE-Ab12Cd (2)._adb-tls-connect._tcp\tdevice transport_id:4", "PHONE\tunauthorized", "garbage"} {
		f.Add(line)
	}
	f.Fuzz(func(t *testing.T, line string) {
		d, ok := ParseDeviceLine(line)
		if !ok {
			return
		}
		if d.Serial == "" || d.State == "" || strings.ContainsAny(d.Serial, "\r\n\x00") {
			t.Fatalf("invalid accepted device %+v", d)
		}
		if IsServiceTransport(d.Serial) && !hasUSB(d.Attributes) && TransportKind(d.Serial, d.Attributes) == "usb" {
			t.Fatalf("service token became USB: %+v", d)
		}
	})
}
