package app

import "testing"

func TestVersionLess(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"v2.1.22", "v2.1.0", false}, // 本地（开发版）更新 → 已最新
		{"v2.1.0", "v2.1.0", false},  // 相等
		{"v2.1.0", "v2.2.0", true},
		{"v2.9.9", "v2.10.0", true},   // 数字比较（非字典序）
		{"v2.1.21", "v2.1.22", true},
		{"2.1.0", "v2.1.1", true},     // 容错：无 v 前缀
		{"garbage", "v2.1.1", false},  // 解析失败=false
		{"v2.1.22", "garbage", false},
		{"v1.9.2", "v2.0.0", true},    // 跨大版本
	}
	for _, c := range cases {
		if got := versionLess(c.cur, c.latest); got != c.want {
			t.Errorf("versionLess(%q,%q)=%v want %v", c.cur, c.latest, got, c.want)
		}
	}
}

func TestParseVerTri(t *testing.T) {
	if parseVerTri("v2.1.22") == nil || parseVerTri("v2.1.22")[2] != 22 {
		t.Error("parseVerTri(v2.1.22) 解析失败")
	}
	if parseVerTri("2.10.3")[1] != 10 {
		t.Error("parseVerTri 数字解析失败")
	}
	if parseVerTri("abc") != nil {
		t.Error("parseVerTri 应返回 nil")
	}
}
