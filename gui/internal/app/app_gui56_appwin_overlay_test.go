package app

// v2.1.56：应用窗口（虚拟屏）注入参数控件可见性——跟随全局设置。
// 此前 StartAppWin 未注入 SCEZ_PARAM_OVERLAY → scrcpy 客户端回退历史默认
// （可见）——表现为"GUI 设置里关了参数控件，主投屏不显示、应用窗口却默认
// 开着"。修复=与主投屏 StartCast 同链读 ShowParamOverlay 显式注入。
// 关键断言：Set 必须为 true（不注入 = 客户端默认可见，等于没修）。

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

func TestAppWinOverlayFollowsSetting(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "MODEL123", State: "device", ConnType: "usb"}})

	// 出厂默认（ShowParamOverlay=true）→ 显式注入可见。
	if err := e.a.StartAppWin("MODEL123", "pkg.one", "一"); err != nil {
		t.Fatal(err)
	}
	p := e.f.waitParams(t, 1)
	if !p.OverlayVisibleSet || !p.OverlayVisible {
		t.Fatalf("默认应注入可见（Set=true Visible=true）: %+v", p)
	}

	// 关闭参数控件 → 新会话注入隐藏（关键：Set=true 才能覆盖客户端历史默认）。
	if err := e.a.SetSettings(false, false); err != nil {
		t.Fatal(err)
	}
	if err := e.a.StartAppWin("MODEL123", "pkg.two", "二"); err != nil {
		t.Fatal(err)
	}
	p2 := e.f.waitParams(t, 2)
	if !p2.OverlayVisibleSet || p2.OverlayVisible {
		t.Fatalf("设置关 → 应注入隐藏（Set=true Visible=false）: %+v", p2)
	}
}
