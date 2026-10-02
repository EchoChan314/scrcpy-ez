package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

const catalogMarker = "SCEZ_APP_CATALOG:"
const maxIconBytes = 512 << 10
const maxIconBatchBytes = 1 << 20
const iconBatchSize = 24

type appCatalog struct {
	Serial    string            `json:"serial"`
	Config    string            `json:"config"`
	Resources []json.RawMessage `json:"resources"`
	Items     []struct {
		Pkg     string `json:"pkg"`
		Name    string `json:"name"`
		Sys     bool   `json:"sys"`
		Version int64  `json:"version"`
		Updated int64  `json:"updated"`
		Icon    int    `json:"icon"`
	} `json:"items"`
}

func parseAppCatalog(out string) (string, []AppListItem, error) {
	start := strings.Index(out, catalogMarker)
	if start < 0 {
		return "", nil, errors.New("app catalog missing")
	}
	var catalog appCatalog
	line := strings.SplitN(out[start+len(catalogMarker):], "\n", 2)[0]
	if len(line) > 2<<20 {
		return "", nil, errors.New("app catalog too large")
	}
	if err := json.Unmarshal([]byte(line), &catalog); err != nil {
		return "", nil, err
	}
	if catalog.Config == "" || catalog.Items == nil || len(catalog.Items) > appListMaxApps {
		return "", nil, errors.New("incomplete app catalog")
	}
	resources := make([]string, 0, len(catalog.Resources))
	for _, resource := range catalog.Resources {
		resources = append(resources, string(resource))
	}
	sort.Strings(resources)
	config := catalog.Config + "|" + strings.Join(resources, "|")
	items := make([]AppListItem, 0, len(catalog.Items))
	seen := map[string]bool{}
	for _, item := range catalog.Items {
		if !rePkgName.MatchString(item.Pkg) || seen[item.Pkg] || len(item.Name) > 4096 || item.Version < 0 || item.Updated < 0 {
			return "", nil, errors.New("invalid app catalog item")
		}
		seen[item.Pkg] = true
		// Include label: some launchable names/icons depend on configuration or package data.
		stamp := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d|%d|%d", config, item.Pkg, item.Name, item.Version, item.Updated, item.Icon)))
		items = append(items, AppListItem{Pkg: item.Pkg, Name: item.Name, Sys: item.Sys, IconStamp: fmt.Sprintf("%x", stamp)})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Sys != items[j].Sys {
			return items[i].Sys
		}
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].Pkg < items[j].Pkg
	})
	return adb.StableSerial(catalog.Serial), items, nil
}

// A content-addressed helper never replaces the server file used by a live cast.
func (a *App) appCatalogServer() (string, string, error) {
	local := filepath.Join(filepath.Dir(a.cfg.BatPath), "scrcpy-server")
	b, err := os.ReadFile(local)
	if err != nil {
		return "", "", err
	}
	hash := sha256.Sum256(b)
	return local, fmt.Sprintf("/data/local/tmp/scrcpy-ez-apps-%x", hash[:8]), nil
}

func (a *App) listAppCatalogOnce(identity, serial string) ([]AppListItem, string, error) {
	local, remote, err := a.appCatalogServer()
	if err != nil {
		return nil, "", err
	}
	query := func() (string, []AppListItem, error) {
		ctx, cancel := context.WithTimeout(context.Background(), appListTimeout)
		defer cancel()
		out, err := a.adb.ShellOut(ctx, serial, "CLASSPATH="+remote+" app_process / com.genymobile.scrcpy.Server "+serverVersion+" cleanup=false app_catalog=true")
		if err != nil {
			return "", nil, err
		}
		return parseAppCatalog(out)
	}
	physical, items, err := query()
	if err != nil && a.appListKeyFor(serial) == identity {
		ctx, cancel := context.WithTimeout(context.Background(), iconPushTimeout)
		err = a.adb.PushFile(ctx, serial, local, remote)
		cancel()
		if err == nil {
			physical, items, err = query()
		}
	}
	if err != nil {
		return nil, "", err
	}
	if a.appListKeyFor(serial) != identity {
		return nil, "", errors.New("device owner changed")
	}
	if physical != "" {
		if entry, ok := a.profiles.Entry(identity); !ok || !contains(entry.Serials, physical) {
			return nil, "", errors.New("app catalog physical identity mismatch")
		}
	}
	return items, remote, nil
}

type iconRecord struct {
	Stamp  string `json:"stamp"`
	Digest string `json:"digest"`
}
type iconIndex map[string]iconRecord

func readIconIndex(dir string) iconIndex {
	index := iconIndex{}
	b, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err == nil && len(b) <= 1<<20 {
		if json.Unmarshal(b, &index) != nil {
			index = iconIndex{}
		}
	}
	if index == nil {
		index = iconIndex{}
	}
	return index
}

func writeIconIndex(dir string, index iconIndex) error {
	b, err := json.Marshal(index)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".index-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	_, err = f.Write(b)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, filepath.Join(dir, "index.json"))
}

func validatedIconDigest(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() <= 0 || info.Size() > maxIconBytes {
		return "", errors.New("invalid icon size")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 512 || cfg.Height > 512 {
		return "", errors.New("invalid icon dimensions")
	}
	if _, err = png.Decode(bytes.NewReader(b)); err != nil {
		return "", err
	}
	hash := sha256.Sum256(b)
	return fmt.Sprintf("%x", hash), nil
}

// Run only in the background catalog job. No PNG scans on the 700ms state path.
func planAppIcons(dir string, items []AppListItem) (needed, removed []string) {
	index := readIconIndex(dir)
	current := map[string]bool{}
	for _, item := range items {
		current[item.Pkg] = true
		digest, err := validatedIconDigest(filepath.Join(dir, item.Pkg+".png"))
		if record := index[item.Pkg]; err != nil || record.Stamp != item.IconStamp || record.Digest != digest {
			needed = append(needed, item.Pkg)
		}
	}
	// Discover removals from the directory too, including caches predating index.json.
	entries, _ := os.ReadDir(dir)
	removedSet := map[string]bool{}
	for _, entry := range entries {
		pkg := strings.TrimSuffix(entry.Name(), ".png")
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".png") && rePkgName.MatchString(pkg) && !current[pkg] {
			removedSet[pkg] = true
		}
	}
	for pkg := range index {
		if !current[pkg] && rePkgName.MatchString(pkg) {
			removedSet[pkg] = true
		}
	}
	for pkg := range removedSet {
		removed = append(removed, pkg)
	}
	sort.Strings(needed)
	sort.Strings(removed)
	return
}

func sameAppMetadata(old, current []AppListItem) bool {
	if len(old) != len(current) {
		return false
	}
	byPackage := make(map[string]AppListItem, len(old))
	for _, item := range old {
		byPackage[item.Pkg] = item
	}
	for _, item := range current {
		if prior, ok := byPackage[item.Pkg]; !ok || prior != item {
			return false
		}
		delete(byPackage, item.Pkg)
	}
	return len(byPackage) == 0
}

// Only successful, valid PNGs gain a stamp. Interrupted/partial transfers stay dirty for the next check.
func (a *App) commitIconDelta(identity, serial, stage string, items []AppListItem, wanted, removed []string) ([]string, error) {
	if a.appListKeyFor(serial) != identity {
		return nil, nil
	}
	dir := a.iconsDirFor(identity)
	index := readIconIndex(dir)
	stamps := map[string]string{}
	for _, item := range items {
		stamps[item.Pkg] = item.IconStamp
	}
	valid := []string{}
	for _, pkg := range wanted {
		if !rePkgName.MatchString(pkg) {
			continue
		}
		digest, err := validatedIconDigest(filepath.Join(stage, pkg+".png"))
		if err != nil {
			continue
		}
		valid = append(valid, pkg)
		index[pkg] = iconRecord{Stamp: stamps[pkg], Digest: digest}
	}
	committed, err := a.commitAppIcons(identity, serial, stage, valid, removed)
	if err != nil || !committed {
		return nil, err
	}
	for _, pkg := range removed {
		delete(index, pkg)
	}
	if err = writeIconIndex(dir, index); err != nil {
		return valid, err
	}
	return valid, nil
}

type AppIconBatch struct {
	Icons     map[string]string `json:"icons"`
	Remaining []string          `json:"remaining,omitempty"`
}

// At most 24 icons and 1MiB per bridge response. Large batches continue in another response.
func (a *App) GetAppIcons(serial string, pkgs []string) (AppIconBatch, error) {
	result := AppIconBatch{Icons: map[string]string{}}
	if len(pkgs) > iconBatchSize {
		return result, errors.New("icon batch too large")
	}
	identity := a.appListKeyFor(serial)
	if identity == "" {
		return result, nil
	}
	dir := a.iconsDirFor(identity)
	budget := maxIconBatchBytes
	for _, pkg := range pkgs {
		if !rePkgName.MatchString(pkg) {
			continue
		}
		path := filepath.Join(dir, pkg+".png")
		info, err := os.Stat(path)
		if err != nil || info.Size() <= 0 || info.Size() > maxIconBytes {
			continue
		}
		if base64.StdEncoding.EncodedLen(int(info.Size()))+22 > budget {
			result.Remaining = append(result.Remaining, pkg)
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		data := "data:image/png;base64," + base64.StdEncoding.EncodeToString(b)
		if len(data) > budget {
			result.Remaining = append(result.Remaining, pkg)
			continue
		}
		budget -= len(data)
		result.Icons[pkg] = data
	}
	return result, nil
}

func (a *App) markIconsChanged(identity string, pkgs []string) {
	if len(pkgs) == 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.appIconsChanged == nil {
		a.appIconsChanged = map[string][]string{}
	}
	a.appIconsChanged[identity] = append(a.appIconsChanged[identity], pkgs...)
}

// Keep Windows command lines bounded even with many unusually long package names.
func iconExportChunks(pkgs []string) [][]string {
	var chunks [][]string
	var chunk []string
	size := 0
	for _, pkg := range pkgs {
		if len(chunk) > 0 && size+len(pkg)+1 > 12000 {
			chunks = append(chunks, chunk)
			chunk = nil
			size = 0
		}
		chunk = append(chunk, pkg)
		size += len(pkg) + 1
	}
	if len(chunk) > 0 {
		chunks = append(chunks, chunk)
	}
	return chunks
}

func (a *App) cleanRemoteIconStage(serial, remote string) {
	// remote is generated internally with a fixed prefix and random hexadecimal suffix.
	if !strings.HasPrefix(remote, "/data/local/tmp/scrcpy/icons-job-") {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = a.adb.ShellOut(ctx, serial, "rm -rf "+remote)
}
