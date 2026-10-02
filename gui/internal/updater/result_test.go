package updater

import (
	"os"
	"path/filepath"
	"testing"
)

func resultOptions(t *testing.T) Options {
	t.Helper()
	root := t.TempDir()
	return Options{Install: root, Cache: filepath.Join(root, "cache"), Version: "v2.2.2"}
}

func putResult(t *testing.T, opts Options, result Result) {
	t.Helper()
	if err := WriteJSON(filepath.Join(opts.Install, ".ez-update-result.json"), result); err != nil {
		t.Fatal(err)
	}
}

func TestSuccessResultOnlyOnFirstRun(t *testing.T) {
	opts := resultOptions(t)
	result := Result{Version: "2.2.2", OK: true, Time: 100}
	putResult(t, opts, result)
	m := New(opts)
	for i := 0; i < 3; i++ {
		state := m.State()
		if state.Result == nil || *state.Result != result {
			t.Fatalf("first run lost result: %+v", state.Result)
		}
		state.Result.Version = "changed by caller"
	}
	if _, err := os.Stat(filepath.Join(opts.Install, ".ez-update-result.json")); !os.IsNotExist(err) {
		t.Fatalf("success result was not consumed: %v", err)
	}
	if result := New(opts).State().Result; result != nil {
		t.Fatalf("second run repeated result: %+v", result)
	}
	m.DismissResult()
	if result := m.State().Result; result != nil {
		t.Fatalf("dismissed result reappeared: %+v", result)
	}
}

func TestSuccessResultCanArriveAfterGUIStarts(t *testing.T) {
	opts := resultOptions(t)
	m := New(opts)
	if m.State().Result != nil {
		t.Fatal("unexpected initial result")
	}
	putResult(t, opts, Result{Version: opts.Version, OK: true, Time: 101})
	if result := m.State().Result; result == nil || !result.OK {
		t.Fatalf("late helper result was lost: %+v", result)
	}
	if New(opts).State().Result != nil {
		t.Fatal("late result repeated after restart")
	}
}

func TestConsumedReceiptPreventsRepeatsButAllowsNextUpdate(t *testing.T) {
	opts := resultOptions(t)
	result := Result{Version: opts.Version, OK: true, Time: 102}
	putResult(t, opts, result)
	if New(opts).State().Result == nil {
		t.Fatal("first success missing")
	}
	// Simulate an original result left behind or restored after consumption.
	putResult(t, opts, result)
	if New(opts).State().Result != nil {
		t.Fatal("consumed receipt repeated")
	}
	result.Time++
	putResult(t, opts, result)
	if New(opts).State().Result == nil {
		t.Fatal("a new installation result was incorrectly suppressed")
	}
}

func TestSuccessResultMustMatchRunningVersion(t *testing.T) {
	for _, version := range []string{"v2.2.1", "v2.2.3"} {
		t.Run(version, func(t *testing.T) {
			opts := resultOptions(t)
			putResult(t, opts, Result{Version: version, OK: true, Time: 103})
			if New(opts).State().Result != nil {
				t.Fatal("success reported for a different running version")
			}
			if version == "v2.2.3" {
				opts.Version = version
				if New(opts).State().Result == nil {
					t.Fatal("older running GUI consumed a newer result")
				}
			} else {
				putResult(t, opts, Result{Version: opts.Version, OK: true, Time: 105})
				if New(opts).State().Result == nil {
					t.Fatal("current success after stale receipt was lost")
				}
			}
		})
	}
}

func TestFailureResultPersistsUntilDismissed(t *testing.T) {
	opts := resultOptions(t)
	result := Result{Version: "v2.2.3", Error: "synthetic install failure", Time: 104}
	putResult(t, opts, result)
	for i := 0; i < 2; i++ {
		if got := New(opts).State().Result; got == nil || *got != result {
			t.Fatalf("failure result was lost: %+v", got)
		}
	}
	m := New(opts)
	m.State()
	m.DismissResult()
	if m.State().Result != nil || New(opts).State().Result != nil {
		t.Fatal("dismissed failure reappeared")
	}
}
