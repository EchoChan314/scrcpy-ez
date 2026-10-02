package app

import (
	"strings"

	"scrcpy-ez/gui/internal/adb"
)

// appWinNoSystemDecorations only reads device metadata already enriched by ADB.
// Xiaomi's secondary launcher shares density/gesture state with its physical
// launcher on the tested firmware. Never re-enable it through the setting for
// other brands. Untested manufacturers retain the previous display policy.
func (a *App) appWinNoSystemDecorations(identity string) bool {
	a.mu.RLock()
	devices := append([]adb.Device(nil), a.devices...)
	a.mu.RUnlock()

	manufacturer, marketname := "", ""
	for _, d := range devices {
		if a.appListKeyFor(d.Serial) != identity {
			continue
		}
		if marketname == "" {
			marketname = d.Marketname
		}
		if strings.TrimSpace(d.Manufacturer) != "" {
			manufacturer = d.Manufacturer
			break
		}
	}
	if e, ok := a.profiles.Entry(identity); ok {
		if strings.TrimSpace(manufacturer) == "" {
			manufacturer = e.Manufacturer
		}
		if marketname == "" {
			marketname = e.Marketname
		}
	}
	return isXiaomiDevice(manufacturer, marketname) || !a.settings.Get().OtherAppWinSystemDecorations
}

func isXiaomiDevice(manufacturer, marketname string) bool {
	manufacturer = strings.ToLower(strings.TrimSpace(manufacturer))
	if manufacturer != "" {
		return manufacturer == "xiaomi" || manufacturer == "redmi" || manufacturer == "poco"
	}
	// The immutable market name is a fallback for older archives or enrichment
	// still in progress. User display names, IP addresses and model numbers are
	// deliberately not used to classify a manufacturer.
	marketname = strings.ToLower(strings.TrimSpace(marketname))
	for _, brand := range []string{"xiaomi", "redmi", "poco"} {
		if marketname == brand || strings.HasPrefix(marketname, brand+" ") {
			return true
		}
	}
	return strings.HasPrefix(marketname, "小米") || strings.HasPrefix(marketname, "红米")
}
