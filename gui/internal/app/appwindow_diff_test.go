package app

// v2.1.16 列表 diff 单测：appListDiff 的 pkg 集合 + 名称映射比对（顺序无关）。
// 覆盖：一致（乱序）/新增/卸载/改名/混合/首次/双空/仅卸载。

import (
	"reflect"
	"testing"
)

func TestAppListDiffSame(t *testing.T) {
	old := []AppListItem{{Pkg: "a", Name: "A"}, {Pkg: "b", Name: "B"}}
	cur := []AppListItem{{Pkg: "b", Name: "B"}, {Pkg: "a", Name: "A"}} // 顺序打乱
	same, added, removed, renamed := appListDiff(old, cur)
	if !same || len(added)+len(removed)+len(renamed) != 0 {
		t.Fatalf("应判定一致：same=%v added=%v removed=%v renamed=%v", same, added, removed, renamed)
	}
}

func TestAppListDiffAddedRemovedRenamed(t *testing.T) {
	old := []AppListItem{{Pkg: "a", Name: "A"}, {Pkg: "b", Name: "B"}, {Pkg: "c", Name: "C"}}
	cur := []AppListItem{{Pkg: "a", Name: "A"}, {Pkg: "b", Name: "B2"}, {Pkg: "d", Name: "D"}}
	same, added, removed, renamed := appListDiff(old, cur)
	if same {
		t.Fatal("不应判定一致")
	}
	if !reflect.DeepEqual(added, []string{"d"}) {
		t.Errorf("added=%v，期望 [d]", added)
	}
	if !reflect.DeepEqual(removed, []string{"c"}) {
		t.Errorf("removed=%v，期望 [c]", removed)
	}
	if !reflect.DeepEqual(renamed, []string{"b"}) {
		t.Errorf("renamed=%v，期望 [b]", renamed)
	}
}

func TestAppListDiffFirstTime(t *testing.T) {
	// 首次（旧档为空）：全部视为新增（触发全量路径）。
	same, added, _, _ := appListDiff(nil, []AppListItem{{Pkg: "a", Name: "A"}, {Pkg: "b", Name: "B"}})
	if same || len(added) != 2 {
		t.Fatalf("首次应全部新增：same=%v added=%v", same, added)
	}
}

func TestAppListDiffEmptyBoth(t *testing.T) {
	same, added, removed, renamed := appListDiff(nil, nil)
	if !same || len(added)+len(removed)+len(renamed) != 0 {
		t.Fatalf("双空应一致：same=%v", same)
	}
}

func TestAppListDiffOnlyRemoved(t *testing.T) {
	// 仅卸载：added/renamed 空（走"仅删本地 PNG"路径）。
	old := []AppListItem{{Pkg: "a", Name: "A"}, {Pkg: "b", Name: "B"}}
	cur := []AppListItem{{Pkg: "a", Name: "A"}}
	same, added, removed, renamed := appListDiff(old, cur)
	if same || len(added) != 0 || len(renamed) != 0 || !reflect.DeepEqual(removed, []string{"b"}) {
		t.Fatalf("仅卸载判定错误：same=%v added=%v removed=%v renamed=%v", same, added, removed, renamed)
	}
}

func TestAppListDiffOnlyAdded(t *testing.T) {
	old := []AppListItem{{Pkg: "a", Name: "A"}}
	cur := []AppListItem{{Pkg: "a", Name: "A"}, {Pkg: "z", Name: "Z"}}
	same, added, removed, renamed := appListDiff(old, cur)
	if same || !reflect.DeepEqual(added, []string{"z"}) || len(removed) != 0 || len(renamed) != 0 {
		t.Fatalf("新增判定错误：same=%v added=%v removed=%v renamed=%v", same, added, removed, renamed)
	}
}

func TestAppListUpdateRequired(t *testing.T) {
	if appListUpdateRequired(true, true) {
		t.Fatal("silent click with no difference must leave all list state untouched")
	}
	if !appListUpdateRequired(true, false) {
		t.Fatal("silent click with a difference must update the list")
	}
	if !appListUpdateRequired(false, true) {
		t.Fatal("ready-edge enumeration must retain its existing update behavior")
	}
}
