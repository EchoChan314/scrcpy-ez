package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

func largeCatalogFixture(count int) string {
	items := make([]map[string]interface{}, count)
	for i := range items {
		items[i] = map[string]interface{}{"pkg": fmt.Sprintf("com.example.app%d", i), "name": fmt.Sprintf("App %05d", i), "version": 1, "updated": 1}
	}
	b, _ := json.Marshal(map[string]interface{}{"serial": "PHONE_A", "config": "test", "items": items})
	return catalogMarker + string(b)
}

func appReadTestApp(t *testing.T) *App {
	t.Helper()
	a, _ := multiTestApp()
	a.profiles.path = filepath.Join(t.TempDir(), "profiles.json")
	a.profiles.SyncDevices([]adb.Device{identityPhone("PHONE_A")})
	a.appReadOps.helper = func() (appServerHelper, error) {
		return appServerHelper{remote: "/data/local/tmp/test-helper", version: "5.0.1"}, nil
	}
	return a
}

func TestAppReadColdHelperDeploymentAndWarmReuse(t *testing.T) {
	a := appReadTestApp(t)
	present, queries, pushes := false, 0, 0
	a.appReadOps.shell = func(ctx context.Context, serial, command string, line func(string)) (string, error) {
		queries++
		if !strings.Contains(command, "if [ ! -s /data/local/tmp/test-helper ]; then echo "+appHelperMissingMarker) {
			t.Fatal("no shell preflight before app_process", command)
		}
		if !present {
			return appHelperMissingMarker + "\r\n", errors.New("exit status 66")
		}
		return largeCatalogFixture(12), nil
	}
	a.appReadOps.push = func(context.Context, string, string, string) error { pushes++; present = true; return nil }
	for attempt := 0; attempt < 2; attempt++ {
		items, _, err := a.listAppCatalogOnce("device:PHONE_A", "PHONE_A")
		if err != nil || len(items) != 12 {
			t.Fatal("fresh helper never deployed", items, err)
		}
	}
	if pushes != 1 || queries != 3 {
		t.Fatal("cold install/warm reuse", pushes, queries)
	}
}

func TestAppReadHelperPushFailureAndUnrelatedErrors(t *testing.T) {
	for _, test := range []struct {
		name, output string
		err          error
		wantPush     int
	}{
		{"missing", appHelperMissingMarker, errors.New("exit status 66"), 1},
		{"timeout", "", context.DeadlineExceeded, 0},
		{"invalid catalog", catalogMarker + `{"config":"x","items":[{"pkg":"../escape"}]}`, nil, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := appReadTestApp(t)
			pushes := 0
			a.appReadOps.shell = func(context.Context, string, string, func(string)) (string, error) { return test.output, test.err }
			a.appReadOps.push = func(context.Context, string, string, string) error {
				pushes++
				return errors.New("transfer disconnected")
			}
			_, _, err := a.listAppCatalogOnce("device:PHONE_A", "PHONE_A")
			s, _ := a.IsAppListCheckBusy("device:PHONE_A")
			if err == nil || pushes != test.wantPush || s.Busy || s.Phase != "error" {
				t.Fatal(err, pushes, s)
			}
		})
	}
}

func TestAppReadCatalogScaleAndResourceGuards(t *testing.T) {
	for _, n := range []int{0, 1, 160, 500, 1000, 5000, 10000} {
		_, items, err := parseAppCatalog(largeCatalogFixture(n))
		if err != nil || len(items) != n {
			t.Fatalf("count=%d parsed=%d err=%v", n, len(items), err)
		}
	}
	for _, out := range []string{largeCatalogFixture(10001), catalogMarker + strings.Repeat(" ", appCatalogBytesLimit+1)} {
		if _, _, err := parseAppCatalog(out); err == nil {
			t.Fatal("resource guard missing")
		}
	}
}

func TestAppReadDynamicBudgetsAndProgressDeadlines(t *testing.T) {
	for _, wifi := range []bool{false, true} {
		light, heavy := appCatalogBudget(20, wifi, 0), appCatalogBudget(1000, wifi, 0)
		if heavy <= light || appCatalogBudget(1000, wifi, 200*time.Millisecond) <= heavy {
			t.Fatal("budget does not adapt to count/history")
		}
		if appCatalogBudget(100000, wifi, time.Second) > 5*time.Minute || appIconBudget(10000, wifi, time.Second) > 2*time.Minute {
			t.Fatal("missing hard bound")
		}
	}
	if appCatalogBudget(1000, true, 0) <= appCatalogBudget(1000, false, 0) || appTransferBudget(2<<20, true, 16384) <= appTransferBudget(2<<20, false, 0) {
		t.Fatal("slow transport budget missing")
	}
	if !appReadWireless("adb-PHONE-test._adb-tls-connect._tcp") || appReadWireless("PHONE_A") {
		t.Fatal("mDNS transport misclassified")
	}
	if appIconChunkSize(time.Second, true) >= appIconChunkSize(10*time.Millisecond, true) {
		t.Fatal("slow icons need smaller batches")
	}
	for _, line := range []string{"", appProgressMarker + `{"phase":"unknown","current":0,"total":1}`, appProgressMarker + `{"phase":"icons","current":2,"total":1}`} {
		if _, ok := parseAppReadProgress(line); ok {
			t.Fatal("bad progress accepted", line)
		}
	}
	if _, ok := parseAppReadProgress("INFO: " + appProgressMarker + `{"phase":"catalog","current":8,"total":1000}`); !ok {
		t.Fatal("progress rejected")
	}
	// Repeated heartbeats cannot reset the initial idle timeout.
	d := newAppReadDeadline(context.Background(), 20*time.Millisecond, time.Second, false, 0)
	d.phase, d.last, d.total = "catalog", 0, 1000
	d.progress(appReadProgress{Phase: "catalog", Current: 0, Total: 1000})
	select {
	case <-d.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("stalled read never ended")
	}
	d.close()
	// Genuine progress may extend a prediction, but never the absolute deadline.
	d = newAppReadDeadline(context.Background(), time.Second, 2*time.Second, false, 0)
	d.start = time.Now().Add(-3 * time.Second)
	d.progress(appReadProgress{Phase: "catalog", Current: 1, Total: 1000})
	if d.ctx.Err() == nil {
		t.Fatal("progress bypassed hard deadline")
	}
	d.close()
}

func TestAppReadFailureRetryAndObsoleteJobIsolation(t *testing.T) {
	a := appReadTestApp(t)
	identity := "device:PHONE_A"
	j := a.startAppReadJob(identity, "PHONE_A")
	if a.startAppReadJob(identity, "PHONE_A") != nil {
		t.Fatal("duplicate job")
	}
	a.finishAppReadJob(identity, j, context.DeadlineExceeded)
	s, _ := a.IsAppListCheckBusy(identity)
	if s.Busy || s.Phase != "error" || s.Error == "" {
		t.Fatalf("stuck after failure: %+v", s)
	}
	next := a.startAppReadJob(identity, "PHONE_A")
	if next == nil {
		t.Fatal("cannot retry after failure")
	}
	a.finishAppReadJob(identity, j, nil)
	s, _ = a.IsAppListCheckBusy(identity)
	if !s.Busy || s.Job != next.id {
		t.Fatal("old completion cleared retry")
	}
	a.updateAppReadState(identity, next, func(s *appReadState) { s.HasCatalog = true })
	a.finishAppReadJob(identity, next, errors.New("offline"))
	s, _ = a.IsAppListCheckBusy(identity)
	if s.Busy || s.Phase != "partial" || !s.HasCatalog {
		t.Fatal(s)
	}
	j = a.startAppReadJob(identity, "PHONE_A")
	<-a.BeginClose()
	if j.ctx.Err() == nil || a.startAppReadJob(identity, "PHONE_A") != nil {
		t.Fatal("shutdown did not cancel/reject reads")
	}
	a.finishAppReadJob(identity, j, j.ctx.Err())
}

func TestAppReadTransportSwitchKeepsSeparateHistoryAndUSBPriority(t *testing.T) {
	a := appReadTestApp(t)
	const identity = "device:PHONE_A"
	mdns := "adb-PHONE_A-test._adb-tls-connect._tcp"
	j := a.startAppReadJob(identity, mdns)
	j.performance.CatalogPerApp = time.Second
	a.selectAppReadSerial(identity, "PHONE_A", j)
	if j.performance.CatalogPerApp != 0 {
		t.Fatal("wireless history leaked into USB")
	}
	j.performance.CatalogPerApp = time.Millisecond
	a.selectAppReadSerial(identity, mdns, j)
	if j.performance.CatalogPerApp != time.Second {
		t.Fatal("wireless history lost")
	}
	setDevices(a, []adb.Device{{Serial: mdns, Identity: identity, State: "device"}, {Serial: "PHONE_A", Identity: identity, State: "device"}})
	if best := a.bestEnumSerial(identity, mdns); best != "PHONE_A" {
		t.Fatal("mDNS hid available USB", best)
	}
	a.finishAppReadJob(identity, j, nil)
}

func TestAppReadThousandAppsVisibleBeforeIconsAndPartialRetry(t *testing.T) {
	a := appReadTestApp(t)
	const identity = "device:PHONE_A"
	entered, unblock, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var wanted []string
	first := true
	failed := "com.example.app999"
	a.appReadOps.shell = func(ctx context.Context, serial, command string, onLine func(string)) (string, error) {
		if strings.HasPrefix(command, "rm -rf ") {
			return "", nil
		}
		if strings.Contains(command, "app_catalog=true") {
			onLine(appProgressMarker + `{"phase":"catalog","current":1000,"total":1000}`)
			return largeCatalogFixture(1000), nil
		}
		mu.Lock()
		if first {
			first = false
			close(entered)
			mu.Unlock()
			select {
			case <-unblock:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			mu.Lock()
		}
		for _, f := range strings.Fields(command) {
			if strings.HasPrefix(f, "export_app_icons=") {
				wanted = strings.Split(strings.TrimPrefix(f, "export_app_icons="), ",")
			}
		}
		mu.Unlock()
		return "SCEZ_ICON_OWNER:PHONE_A\nExported icons:1", nil
	}
	a.appReadOps.pull = func(ctx context.Context, serial, remote, local string) error {
		mu.Lock()
		pkgs := append([]string(nil), wanted...)
		skip := failed
		mu.Unlock()
		for _, pkg := range pkgs {
			if pkg != skip {
				writeTestPNG(t, filepath.Join(local, pkg+".png"), color.White)
			}
		}
		return nil
	}
	go func() { a.runAppListEnum(identity, "PHONE_A"); close(finished) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("metadata never completed")
	}
	s, _ := a.IsAppListCheckBusy(identity)
	list, _ := a.GetAppList(identity)
	if !s.Busy || !s.HasCatalog || len(list) != 1000 || s.InitialIconBusy {
		t.Fatalf("catalog held behind icons: %+v count=%d", s, len(list))
	}
	close(unblock)
	select {
	case <-finished:
	case <-time.After(15 * time.Second):
		t.Fatal("pipeline stalled")
	}
	s, _ = a.IsAppListCheckBusy(identity)
	if s.Busy || s.Phase != "partial" || s.IconDone != 999 {
		t.Fatalf("partial icons: %+v", s)
	}
	needed, _ := planAppIcons(a.iconsDirFor(identity), list)
	if len(needed) != 1 || needed[0] != failed {
		t.Fatal("successful icons not retained", needed)
	}
	mu.Lock()
	failed = ""
	mu.Unlock()
	a.runAppListEnum(identity, "PHONE_A")
	s, _ = a.IsAppListCheckBusy(identity)
	if s.Busy || s.Phase != "complete" || s.IconTotal != 1 || s.IconDone != 1 {
		t.Fatalf("retry should fetch only missing icon: %+v", s)
	}
	if needed, _ := planAppIcons(a.iconsDirFor(identity), list); len(needed) != 0 {
		t.Fatal("retry incomplete", needed)
	}
	b, err := json.Marshal(s)
	if err != nil || !strings.Contains(string(b), `"hasCatalog":true`) {
		t.Fatal("status fields not exposed to bridge", string(b), err)
	}
}

func TestAppReadIndividualMetadataFailureKeepsPriorRecord(t *testing.T) {
	a := appReadTestApp(t)
	identity := "device:PHONE_A"
	prior := AppListItem{Pkg: "com.example.old", Name: "Old", IconStamp: "retained"}
	a.profiles.SetApps(identity, []AppListItem{prior})
	a.appReadOps.shell = func(context.Context, string, string, func(string)) (string, error) {
		return catalogMarker + `{"serial":"PHONE_A","config":"test","items":[],"failed":["com.example.old"]}`, nil
	}
	j := a.startAppReadJob(identity, "PHONE_A")
	items, _, err := a.listAppCatalogForJob(identity, "PHONE_A", j)
	a.finishAppReadJob(identity, j, err)
	s, _ := a.IsAppListCheckBusy(identity)
	if err != nil || len(items) != 1 || items[0] != prior || s.Phase != "partial" {
		t.Fatal(items, s, err)
	}
}

func TestAppReadInterruptedTransferAndCancellation(t *testing.T) {
	a := appReadTestApp(t)
	const identity = "device:PHONE_A"
	items := []AppListItem{{Pkg: "com.example.a", IconStamp: "v1"}, {Pkg: "com.example.b", IconStamp: "v1"}}
	a.appReadOps.shell = func(context.Context, string, string, func(string)) (string, error) {
		return "SCEZ_ICON_OWNER:PHONE_A\nExported icons:2", nil
	}
	a.appReadOps.pull = func(ctx context.Context, serial, remote, local string) error {
		writeTestPNG(t, filepath.Join(local, "com.example.a.png"), color.White)
		return errors.New("connection lost during pull")
	}
	j := a.startAppReadJob(identity, "PHONE_A")
	changed, err := a.readAppIconBatch(identity, "PHONE_A", appServerHelper{}, items, []string{"com.example.a", "com.example.b"}, t.TempDir(), j)
	a.finishAppReadJob(identity, j, err)
	if err == nil || len(changed) != 1 || changed[0] != "com.example.a" {
		t.Fatal("partial transfer lost valid data", changed, err)
	}
	needed, _ := planAppIcons(a.iconsDirFor(identity), items)
	if len(needed) != 1 || needed[0] != "com.example.b" {
		t.Fatal("interrupted file gained a success stamp", needed)
	}
	started, done := make(chan struct{}), make(chan error, 1)
	a.appReadOps.shell = func(ctx context.Context, serial, command string, line func(string)) (string, error) {
		if strings.HasPrefix(command, "rm -rf ") {
			return "", nil
		}
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	j = a.startAppReadJob(identity, "PHONE_A")
	stage := t.TempDir()
	go func() {
		_, err := a.readAppIconBatch(identity, "PHONE_A", appServerHelper{}, items, needed, stage, j)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("read did not start")
	}
	j.cancel()
	select {
	case err = <-done:
	case <-time.After(time.Second):
		t.Fatal("cancel did not interrupt I/O")
	}
	a.finishAppReadJob(identity, j, err)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s, _ := a.IsAppListCheckBusy(identity)
	if s.Busy {
		t.Fatal("canceled job kept the busy gate")
	}
}
