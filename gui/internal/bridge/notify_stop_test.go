package bridge

import (
	"strings"
	"testing"
)

func TestShellNotificationKeys(t *testing.T) {
	out := "0|com.android.shell|5456730|null|2000\r\n" +
		"0|com.miui.securitycenter|20006|null|1000\r\n" +
		"0|com.android.shell|123|null|2000\r\n" +
		"\r\n" +
		"0|com.xiaomi.mi_connect_service|1|null|1000\r\n"
	got := shellNotificationKeys(out)
	want := []string{
		"0|com.android.shell|5456730|null|2000",
		"0|com.android.shell|123|null|2000",
	}
	if len(got) != len(want) {
		t.Fatalf("keys=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("keys[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

// 渠道定义之类的噪音行不含 "|<pkg>|" 形态，不应被当成通知。
func TestShellNotificationKeysIgnoresNoise(t *testing.T) {
	out := "AppSettings: com.android.shell (2000)\n" +
		"NotificationChannel{mId='scrcpy_ez_mirroring'}\n" +
		"  mId=scrcpy_ez_mirroring\n"
	if got := shellNotificationKeys(out); len(got) != 0 {
		t.Fatalf("应忽略噪音行，实际 %v", got)
	}
}

func TestNotificationsGone(t *testing.T) {
	baseline := []string{"0|com.android.shell|5456730|null|2000"}
	if notificationsGone("0|com.android.shell|5456730|null|2000\n0|other|1|null|1", baseline) {
		t.Fatal("通知仍在，应判未撤下")
	}
	if !notificationsGone("0|other|1|null|1\n", baseline) {
		t.Fatal("基线通知已消失，应判撤下")
	}
	if !notificationsGone("", nil) {
		t.Fatal("空基线应判撤下")
	}
}

func TestClosedFlagPath(t *testing.T) {
	if closedFlagPath("") != "" {
		t.Fatal("空 tag 不应产生标记路径")
	}
	p := closedFlagPath("SCRCPY_LAUNCH_USB_WATCH_123")
	if !strings.HasSuffix(p, "scrcpy_closed_SCRCPY_LAUNCH_USB_WATCH_123.flag") {
		t.Fatalf("标记路径不符: %s", p)
	}
	if closedFlagName("T") != "scrcpy_closed_T.flag" {
		t.Fatalf("标记名不符: %s", closedFlagName("T"))
	}
}
