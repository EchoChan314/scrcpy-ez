package app

import (
	"os"
	"path/filepath"
	"testing"
)

// v2.1.24 回归：apps/iconsFullAt 必须经「写盘 → Load（legacy 中转）」往返保留。
// 历史 bug：Load 走 legacyDeviceEntry 中转——该结构缺字段被静默丢弃，
// 表现为"每次 GUI 重启都判全量超期 → 重导图标 9-14s + 列表 diff 全量"。
func TestLoadKeepsAppsAndIconsFullAt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "profiles.json")
	raw := `{"devices":{"Dev 1":{"marketname":"Dev 1","res":"1080x1920","serials":["s1"],"addrs":[],` +
		`"apps":[{"pkg":"com.a.b","name":"AB","sys":true}],"iconsFullAt":123456}}}`
	if err := os.WriteFile(p, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	s := NewProfileStore(p)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	e, ok := s.Entry(fixtureArchiveKey(s, "Dev 1"))
	if !ok {
		t.Fatal("档案未加载")
	}
	if len(e.Apps) != 1 || e.Apps[0].Pkg != "com.a.b" || e.Apps[0].Name != "AB" || !e.Apps[0].Sys {
		t.Fatalf("apps 往返丢失: %+v", e.Apps)
	}
	if e.IconsFullAt != 123456 {
		t.Fatalf("iconsFullAt 往返丢失: %d", e.IconsFullAt)
	}
}
