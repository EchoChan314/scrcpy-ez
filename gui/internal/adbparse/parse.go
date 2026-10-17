// Package adbparse contains side-effect-free ADB text parsing shared by the
// device event stream and the property/discovery paths.
package adbparse

import (
	"net"
	"regexp"
	"strconv"
	"strings"
)

type DeviceLine struct {
	Serial, State string
	Attributes    []string
}

var deviceState = regexp.MustCompile(`(?:^|[ \t]+)(device|offline|unauthorized|authorizing|connecting|bootloader|recovery|sideload|rescue|host|detached|unknown|no permissions)(?:[ \t]+|$)`)
var conflictSuffix = regexp.MustCompile(` \([1-9][0-9]*\)$`)

// ParseDeviceLine retains the exact transport token, including DNS escapes and
// collision suffixes. Whitespace inside a service name is not a column boundary.
func ParseDeviceLine(line string) (DeviceLine, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "List of devices") || hasControl(line, true) {
		return DeviceLine{}, false
	}
	// Short ADB snapshots delimit serial and state with a tab. Prefer this
	// authoritative boundary even if a custom instance contains a state word.
	if tab := strings.IndexByte(line, '\t'); tab > 0 {
		if d, ok := parseDeviceAt(line[:tab], strings.TrimSpace(line[tab+1:])); ok {
			return d, true
		}
	}
	// Long output is space aligned. Search from the right so a state word in
	// an instance name cannot truncate the network token into a USB serial.
	var boundaries [][2]int
	for offset := 0; offset < len(line); {
		match := deviceState.FindStringSubmatchIndex(line[offset:])
		if match == nil {
			break
		}
		if start := offset + match[2]; start > 0 {
			boundaries = append(boundaries, [2]int{offset + match[0], start})
		}
		offset += match[3]
	}
	for i := len(boundaries) - 1; i >= 0; i-- {
		boundary := boundaries[i]
		if d, ok := parseDeviceAt(strings.TrimSpace(line[:boundary[0]]), line[boundary[1]:]); ok {
			return d, true
		}
	}
	return DeviceLine{}, false
}

func parseDeviceAt(serial, rest string) (DeviceLine, bool) {
	serial = strings.TrimSpace(serial)
	match := deviceState.FindStringSubmatchIndex(rest)
	if serial == "" || match == nil || match[2] != 0 {
		return DeviceLine{}, false
	}
	d := DeviceLine{Serial: serial, State: rest[match[2]:match[3]], Attributes: strings.Fields(rest[match[3]:])}
	if strings.ContainsAny(serial, " \t") && !IsServiceTransport(serial) && !hasUSB(d.Attributes) {
		return DeviceLine{}, false
	}
	return d, true
}

func hasUSB(attributes []string) bool {
	for _, attr := range attributes {
		if strings.HasPrefix(attr, "usb:") {
			return true
		}
	}
	return false
}

// IsServiceTransport identifies network service tokens without interpreting
// their instance names. Incomplete/unknown ADB service types also cannot be USB.
func IsServiceTransport(serial string) bool {
	s := strings.ToLower(serial)
	return strings.Contains(s, "._adb") || strings.Contains(s, "_adb-tls")
}

// TransportKind preserves the short-snapshot USB fallback while preventing
// service names, malformed endpoints and emulator transports from reaching it.
func TransportKind(serial string, attributes []string) string {
	if hasUSB(attributes) {
		return "usb"
	}
	s := strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(serial), "."), ".local")
	switch {
	case strings.HasPrefix(serial, "emulator"):
		return "other"
	case IsServiceTransport(serial):
		if strings.HasSuffix(s, "._adb-tls-connect._tcp") || strings.HasSuffix(s, "._adb._tcp") {
			return "wifi"
		}
		return "other"
	case isEndpoint(serial):
		return "wifi"
	case strings.ContainsAny(serial, ":\\ \t\r\n\x00"):
		return "other"
	default:
		return "usb"
	}
}

func IsIPEndpoint(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if zone := strings.LastIndexByte(host, '%'); zone >= 0 {
		host = host[:zone]
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= 65535 && net.ParseIP(host) != nil
}

func isEndpoint(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	n, portErr := strconv.Atoi(port)
	return err == nil && portErr == nil && host != "" && n > 0 && n <= 65535 && !hasControl(host, false) && !strings.ContainsAny(host, " \\")
}

func hasControl(s string, allowTab bool) bool {
	for i := 0; i < len(s); i++ {
		if (s[i] < 32 && !(allowTab && s[i] == '\t')) || s[i] == 127 {
			return true
		}
	}
	return false
}

// ServiceInstance is for comparison/identity only. Callers must retain the
// original wire/presentation name for DNS records and ADB command arguments.
func ServiceInstance(name string) string {
	s := decodeDNSName(strings.TrimSpace(name))
	fq := strings.TrimSuffix(s, ".")
	fq = trimSuffixFold(fq, ".local")
	stripped := false
	for {
		next := fq
		for _, suffix := range []string{"._adb-tls-connect._tcp", "._adb-tls-pairing._tcp", "._adb._tcp", "._adb-tls-connect", "._adb-tls-pairing", "._adb", "._tcp"} {
			if strings.HasSuffix(strings.ToLower(fq), suffix) {
				next = fq[:len(fq)-len(suffix)]
				break
			}
		}
		if next == fq {
			break
		}
		fq, stripped = next, true
	}
	if stripped {
		return fq
	}
	return s
}

func trimSuffixFold(s, suffix string) string {
	if strings.HasSuffix(strings.ToLower(s), suffix) {
		return s[:len(s)-len(suffix)]
	}
	return s
}

// ServiceSerial handles ADB-generated names only. Never apply conflict-name
// cleanup to an ordinary USB serial or a QR pairing service chosen by a host.
func ServiceSerial(name string) string {
	s := ServiceInstance(name)
	if !strings.HasPrefix(s, "adb-") {
		return ""
	}
	for {
		next := conflictSuffix.ReplaceAllString(s, "")
		if next == s {
			break
		}
		s = next
	}
	s = strings.TrimPrefix(s, "adb-")
	if i := strings.LastIndexByte(s, '-'); i > 0 && alnum6(s[i+1:]) {
		s = s[:i]
	}
	if strings.ContainsAny(s, ":\\ \t\r\n\x00") || hasControl(s, false) {
		return ""
	}
	return s
}

func alnum6(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, ch := range s {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z') {
			return false
		}
	}
	return true
}

func decodeDNSName(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			out.WriteByte(s[i])
			continue
		}
		if s[i+1] >= '0' && s[i+1] <= '9' && (i+3 >= len(s) || s[i+2] < '0' || s[i+2] > '9' || s[i+3] < '0' || s[i+3] > '9') {
			out.WriteByte('\\')
			continue
		}
		if i+3 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' && s[i+2] >= '0' && s[i+2] <= '9' && s[i+3] >= '0' && s[i+3] <= '9' {
			if n, _ := strconv.Atoi(s[i+1 : i+4]); n <= 255 {
				out.WriteByte(byte(n))
				i += 3
				continue
			}
			// Invalid numeric escapes are not evidence of a device identity.
			out.WriteByte('\\')
			continue
		}
		i++
		out.WriteByte(s[i])
	}
	return out.String()
}
