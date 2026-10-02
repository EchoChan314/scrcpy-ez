package app

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

func catalogFixture(version int, config string, resources []string) string {
	b, _ := json.Marshal(map[string]interface{}{
		"serial": "PHONE_A", "config": config, "resources": resources,
		"items": []map[string]interface{}{
			{"pkg": "com.example.a", "name": "A stable label", "version": version, "updated": 123, "icon": 9},
			{"pkg": "com.example.b", "name": "B", "version": 1, "updated": 123, "icon": 10},
		},
	})
	return "INFO: " + catalogMarker + string(b) + "\nINFO: complete\n"
}

func TestCatalogDetectsSameLabelUpdateWithoutDirtyingOtherApps(t *testing.T) {
	serial, old, err := parseAppCatalog(catalogFixture(1, "config", []string{"overlayB", "overlayA"}))
	if err != nil || serial != "PHONE_A" {
		t.Fatal(serial, err)
	}
	_, reordered, _ := parseAppCatalog(catalogFixture(1, "config", []string{"overlayA", "overlayB"}))
	if !reflect.DeepEqual(old, reordered) {
		t.Fatal("overlay enumeration order dirtied icons")
	}
	_, updated, _ := parseAppCatalog(catalogFixture(2, "config", []string{"overlayA", "overlayB"}))
	if old[0].IconStamp == updated[0].IconStamp || old[1].IconStamp != updated[1].IconStamp {
		t.Fatal("package update did not stay scoped to its own icon")
	}
	if same, _, _, _ := appListDiff(old, updated); !same {
		t.Fatal("metadata-only update must not rebuild the visible list")
	}
	_, themed, _ := parseAppCatalog(catalogFixture(1, "new config", []string{"overlayA", "overlayB"}))
	if old[1].IconStamp == themed[1].IconStamp {
		t.Fatal("resource configuration change was missed")
	}
	for _, invalid := range []string{"", catalogMarker + `{"config":"x"}`, strings.Replace(catalogFixture(1, "c", nil), "com.example.a", "../escape", 1)} {
		if _, _, err := parseAppCatalog(invalid); err == nil {
			t.Fatal("accepted incomplete/invalid catalog")
		}
	}
}

func writeTestPNG(t *testing.T, path string, c color.Color) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	m := image.NewRGBA(image.Rect(0, 0, 8, 8))
	m.Set(0, 0, c)
	if err = png.Encode(f, m); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestIconDeltaColdWarmCorruptPartialAndRestart(t *testing.T) {
	a, _ := multiTestApp()
	a.profiles.path = filepath.Join(t.TempDir(), "profiles.json")
	a.profiles.SyncDevices([]adb.Device{identityPhone("PHONE_A")})
	identity := "device:PHONE_A"
	items := []AppListItem{{Pkg: "com.example.a", Name: "A", IconStamp: "version1"}, {Pkg: "com.example.b", Name: "B", IconStamp: "version1"}}
	dir := a.iconsDirFor(identity)
	wanted, removed := planAppIcons(dir, items)
	if len(wanted) != 2 || len(removed) != 0 {
		t.Fatal("empty delta must include all applications", wanted, removed)
	}
	stage := t.TempDir()
	writeTestPNG(t, filepath.Join(stage, "com.example.a.png"), color.White)
	writeTestPNG(t, filepath.Join(stage, "com.example.b.png"), color.Black)
	if changed, err := a.commitIconDelta(identity, "PHONE_A", stage, items, wanted, nil); err != nil || len(changed) != 2 {
		t.Fatal(changed, err)
	}
	if needed, _ := planAppIcons(dir, items); len(needed) != 0 {
		t.Fatal("warm cache re-exported", needed)
	}
	// A 24h timestamp is immaterial; the on-disk index survives a process restart.
	a.profiles.SetIconsFullAt(identity, 1)
	if needed, _ := planAppIcons(dir, items); len(needed) != 0 {
		t.Fatal("wall-clock expiry re-exported unchanged icons")
	}
	before, _ := os.ReadFile(filepath.Join(dir, "com.example.a.png"))
	items[0].IconStamp = "version2"
	if needed, _ := planAppIcons(dir, items); !reflect.DeepEqual(needed, []string{"com.example.a"}) {
		t.Fatal("same-label update was not a delta", needed)
	}
	// Missing/corrupt partial output cannot replace or bless the old icon.
	broken := t.TempDir()
	os.WriteFile(filepath.Join(broken, "com.example.a.png"), []byte("broken PNG"), 0600)
	if changed, err := a.commitIconDelta(identity, "PHONE_A", broken, items, []string{"com.example.a"}, nil); err != nil || len(changed) != 0 {
		t.Fatal(changed, err)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "com.example.a.png")); !reflect.DeepEqual(before, after) {
		t.Fatal("failed update discarded prior icon")
	}
	if needed, _ := planAppIcons(dir, items); !reflect.DeepEqual(needed, []string{"com.example.a"}) {
		t.Fatal("failed output advanced its stamp", needed)
	}
	good := t.TempDir()
	writeTestPNG(t, filepath.Join(good, "com.example.a.png"), color.RGBA{R: 255, A: 255})
	if _, err := a.commitIconDelta(identity, "PHONE_A", good, items, []string{"com.example.a"}, nil); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "com.example.b.png"), []byte("corrupt"), 0600)
	if needed, _ := planAppIcons(dir, items); !reflect.DeepEqual(needed, []string{"com.example.b"}) {
		t.Fatal("corrupt cache was not detected", needed)
	}
	if needed, removed := planAppIcons(dir, items[:1]); len(needed) != 0 || !reflect.DeepEqual(removed, []string{"com.example.b"}) {
		t.Fatal("uninstall delta", needed, removed)
	}
	if _, err := a.commitIconDelta(identity, "PHONE_A", t.TempDir(), items[:1], nil, []string{"com.example.b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "com.example.b.png")); !os.IsNotExist(err) {
		t.Fatal("uninstalled icon remains")
	}
}

func TestIconBatchIsolationValidationAndPhysicalOwner(t *testing.T) {
	a, _ := multiTestApp()
	a.profiles.path = filepath.Join(t.TempDir(), "profiles.json")
	a.profiles.SyncDevices([]adb.Device{identityPhone("PHONE_A"), identityPhone("PHONE_B")})
	writeTestPNG(t, filepath.Join(a.iconsDirFor("device:PHONE_A"), "com.example.app.png"), color.White)
	writeTestPNG(t, filepath.Join(a.iconsDirFor("device:PHONE_B"), "com.example.app.png"), color.Black)
	x, _ := a.GetAppIcons("device:PHONE_A", []string{"com.example.app", "../escape"})
	y, _ := a.GetAppIcons("device:PHONE_B", []string{"com.example.app"})
	if len(x.Icons) != 1 || x.Icons["com.example.app"] == "" || x.Icons["com.example.app"] == y.Icons["com.example.app"] {
		t.Fatal("batch mixed devices", x, y)
	}
	if _, err := a.GetAppIcons("PHONE_A", make([]string, 25)); err == nil {
		t.Fatal("unbounded batch accepted")
	}
	if a.iconExportOwnerMatches("device:PHONE_A", "SCEZ_ICON_OWNER:PHONE_B\n") || a.iconExportOwnerMatches("device:PHONE_A", "Exported icons:1") {
		t.Fatal("unverified physical owner accepted")
	}
	if !a.iconExportOwnerMatches("device:PHONE_A", "INFO: SCEZ_ICON_OWNER:PHONE_A\nExported icons:1") {
		t.Fatal("valid physical owner rejected")
	}
}

func TestIconMetadataSurvivesProfileReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	p := NewProfileStore(path)
	p.SyncDevices([]adb.Device{identityPhone("PHONE_A")})
	items := []AppListItem{{Pkg: "com.example.a", Name: "A", IconStamp: "stamp"}}
	if err := p.SetApps("device:PHONE_A", items); err != nil {
		t.Fatal(err)
	}
	items[0].IconStamp = "updated stamp with the same label"
	if err := p.SetApps("device:PHONE_A", items); err != nil {
		t.Fatal(err)
	}
	loaded := NewProfileStore(path)
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	entry, ok := loaded.Entry("device:PHONE_A")
	if !ok || !reflect.DeepEqual(entry.Apps, items) {
		t.Fatal("reload discarded metadata", entry)
	}
}

func TestLargeIconBatchContinuesWithoutDroppingIcons(t *testing.T) {
	a, _ := multiTestApp()
	a.profiles.path = filepath.Join(t.TempDir(), "profiles.json")
	a.profiles.SyncDevices([]adb.Device{identityPhone("PHONE_A")})
	dir := a.iconsDirFor("device:PHONE_A")
	os.MkdirAll(dir, 0755)
	var pkgs []string
	for i := 0; i < 12; i++ {
		pkg := fmt.Sprintf("com.example.pkg%d", i)
		pkgs = append(pkgs, pkg)
		if err := os.WriteFile(filepath.Join(dir, pkg+".png"), make([]byte, 256<<10), 0600); err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	for len(pkgs) > 0 {
		result, err := a.GetAppIcons("PHONE_A", pkgs)
		if err != nil || len(result.Icons) == 0 {
			t.Fatal("batch made no progress", err)
		}
		bytes := 0
		for _, data := range result.Icons {
			bytes += len(data)
		}
		if bytes > maxIconBatchBytes {
			t.Fatal("bridge payload exceeded cap")
		}
		count += len(result.Icons)
		pkgs = result.Remaining
	}
	if count != 12 {
		t.Fatal("batch silently dropped valid icons", count)
	}
}

func BenchmarkIconDeltaWarm160(b *testing.B) {
	dir := b.TempDir()
	index := iconIndex{}
	items := make([]AppListItem, 160)
	m := image.NewRGBA(image.Rect(0, 0, 144, 144))
	for y := 0; y < 144; y++ {
		for x := 0; x < 144; x++ {
			m.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x * y), A: 255})
		}
	}
	for i := range items {
		pkg := fmt.Sprintf("com.example.pkg%d", i)
		path := filepath.Join(dir, pkg+".png")
		f, err := os.Create(path)
		if err != nil {
			b.Fatal(err)
		}
		err = png.Encode(f, m)
		f.Close()
		if err != nil {
			b.Fatal(err)
		}
		digest, err := validatedIconDigest(path)
		if err != nil {
			b.Fatal(err)
		}
		items[i] = AppListItem{Pkg: pkg, Name: pkg, IconStamp: "current"}
		index[pkg] = iconRecord{Stamp: "current", Digest: digest}
	}
	if err := writeIconIndex(dir, index); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if needed, removed := planAppIcons(dir, items); len(needed)+len(removed) != 0 {
			b.Fatal("warm cache was dirty")
		}
	}
}
