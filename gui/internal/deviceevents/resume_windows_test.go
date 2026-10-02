//go:build windows

package deviceevents

import "testing"

func TestWindowsResumeSubscription(t *testing.T) {
	ch, stop, err := resumeSignals()
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if ch == nil {
		t.Fatal("resume event subscription missing")
	}
}
