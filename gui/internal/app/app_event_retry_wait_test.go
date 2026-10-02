package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
)

func TestEventRetryWaitVisibleAndScoped(t *testing.T) {
	a, rec := multiTestApp()
	setDevices(a, []adb.Device{{Serial: "K80", State: "device", ConnType: "usb"}, {Serial: "Pad", State: "device", ConnType: "usb"}})
	for _, serial := range []string{"K80", "Pad"} {
		if err := a.StartCast(serial); err != nil {
			t.Fatal(err)
		}
		a.NotifyLine(serial, "===== 开始投屏 =====")
	}
	a.NotifyLine("K80", "SCRCPY_EZ_RETRY_WAIT")
	s := sessionBySerial(t, a, "K80")
	if !s.Active || s.Cast.Phase != "retry-wait" || !s.Cast.WaitingInput || s.Cast.Prompt != bridge.PromptRetryQR || s.Cast.Spec != nil {
		t.Fatalf("failed cast must expose retry controls: %+v", s)
	}
	a.NotifyLine("K80", "[会话] route=K80 code=1 (0x00000001)")
	if !sessionBySerial(t, a, "K80").Cast.WaitingInput {
		t.Fatal("diagnostic output hid retry controls")
	}
	if sessionBySerial(t, a, "Pad").Cast.Phase != "casting" || rec.serial("Pad")[0].isStopped() {
		t.Fatal("failure state leaked to the other device")
	}
	a.NotifyLine("K80", "[自动切换] 启动 wifi：192.0.2.1:5555")
	s = sessionBySerial(t, a, "K80")
	if s.Cast.WaitingInput || s.Cast.Prompt != bridge.PromptNone || s.Cast.Phase != "reconnect" {
		t.Fatalf("new device route must clear retry wait: %+v", s)
	}
}

func TestAppWindowEventRetryWait(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb"}})
	if err := e.a.StartAppWin("12345TESTA", "com.android.browser", "浏览器"); err != nil {
		t.Fatal(err)
	}
	e.f.waitStarts(t, 1)
	e.fireLine("SCRCPY_EZ_RETRY_WAIT")
	list := waitAppWins(t, e.a, 1)
	if list[0].Phase != "retry-wait" || list[0].PhaseText == "" {
		t.Fatalf("app window failure not visible: %+v", list[0])
	}
	e.fireLine("INFO: Texture: 1920x1080")
	if list := waitAppWins(t, e.a, 1); list[0].Phase != "" {
		t.Fatalf("ready window must clear wait: %+v", list[0])
	}
}
