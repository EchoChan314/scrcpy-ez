package app

import (
	"strings"
	"testing"
)

func TestUpdateConfirmationIncludesMainAndAppWindows(t *testing.T) {
	a := &App{sessions: map[string]*sessionState{"usb": {runner: &fakeRunner{}}}, appWins: map[string]*appWinState{"wifi#pkg": {runner: &fakeRunner{}}}}
	reply, e := a.InstallUpdate(false, func() { t.Error("quit before confirmation") })
	if e != nil || !reply.NeedsConfirm || reply.ActiveWindows != 2 {
		t.Fatalf("%+v %v", reply, e)
	}
	a.update.installing = true
	if e = a.StartCast("usb"); e == nil || !strings.Contains(e.Error(), "重启更新") {
		t.Fatal("main cast not blocked", e)
	}
	if e = a.StartAppWin("wifi", "pkg", "test"); e == nil || !strings.Contains(e.Error(), "重启更新") {
		t.Fatal("app cast not blocked", e)
	}
}
