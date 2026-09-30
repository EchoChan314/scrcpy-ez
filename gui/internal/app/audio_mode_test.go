package app

import (
	"encoding/json"
	"testing"
)

// v2.1.78 声音档位：归一（前后端共同词表）+ 默认值（应用屏 phone / 主屏 pc）。
func TestNormalizeAudioMode(t *testing.T) {
	cases := []struct{ in, def, want string }{
		{"phone", "pc", "phone"},
		{"pc", "phone", "pc"},
		{"both", "pc", "both"},
		{"", "pc", "pc"},
		{"", "phone", "phone"},
		{"bogus", "pc", "pc"},
	}
	for _, c := range cases {
		if got := NormalizeAudioMode(c.in, c.def); got != c.want {
			t.Fatalf("NormalizeAudioMode(%q, %q) = %q，期望 %q", c.in, c.def, got, c.want)
		}
	}
	var p AppWinModeParams
	if got := p.EffectiveAppWinAudio(); got != "phone" {
		t.Fatalf("应用屏默认档应为 phone（got %q）", got)
	}
	if got := mainAudioMode(""); got != "pc" {
		t.Fatalf("主屏默认档应为 pc（got %q）", got)
	}
	if got := mainAudioMode("both"); got != "both" {
		t.Fatalf("主屏显式 both 应保留（got %q）", got)
	}
}

// v2.1.78 老档案兼容：旧 "mute" 字段已移除（JSON 解码忽略）——mute:true/false 均按
// 应用屏默认 phone 处理（0927 拍板：应用屏默认「不传输到电脑」）；显式 audio 优先。
func TestAppWinAudioLegacyMuteCompat(t *testing.T) {
	var p1 AppWinModeParams
	if err := json.Unmarshal([]byte(`{"size":"1920","mute":true}`), &p1); err != nil {
		t.Fatal(err)
	}
	if p1.EffectiveAppWinAudio() != "phone" {
		t.Fatalf("旧 mute:true 应归 phone（got %q）", p1.EffectiveAppWinAudio())
	}
	var p2 AppWinModeParams
	if err := json.Unmarshal([]byte(`{"size":"1920","mute":false}`), &p2); err != nil {
		t.Fatal(err)
	}
	if p2.EffectiveAppWinAudio() != "phone" {
		t.Fatalf("旧 mute:false 应归默认 phone（got %q）", p2.EffectiveAppWinAudio())
	}
	var p3 AppWinModeParams
	if err := json.Unmarshal([]byte(`{"size":"1920","audio":"both"}`), &p3); err != nil {
		t.Fatal(err)
	}
	if p3.EffectiveAppWinAudio() != "both" {
		t.Fatalf("显式 audio:both 应保留（got %q）", p3.EffectiveAppWinAudio())
	}
}

// normalizeAppWinMode：空套走 def 后仍输出最终档（phone 兜底）——GetAppWinParams 依赖。
func TestNormalizeAppWinModeAudio(t *testing.T) {
	def := AppWinModeParams{Size: "1920", FPS: 60, Bitrate: 15, Flex: true, Audio: "phone"}
	got := normalizeAppWinMode(AppWinModeParams{}, def)
	if got.Audio != "phone" || got.Size != "1920" {
		t.Fatalf("空套归一异常: %+v", got)
	}
	got2 := normalizeAppWinMode(AppWinModeParams{Size: "1280x720", Audio: ""}, def)
	if got2.Audio != "phone" {
		t.Fatalf("音频空应归 phone: %+v", got2)
	}
	got3 := normalizeAppWinMode(AppWinModeParams{Size: "1280", Audio: "both"}, def)
	if got3.Audio != "both" {
		t.Fatalf("显式 both 应保留: %+v", got3)
	}
}
