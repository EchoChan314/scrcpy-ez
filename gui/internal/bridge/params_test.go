package bridge

import (
	"strings"
	"testing"
)

// envMap 把 castEnv 的 "K=V" 列表转成 map（断言与顺序无关）。
func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

// 设置面板开关 A：参数控件启动可见性注入（SCEZ_PARAM_OVERLAY）。
// GUI→bat→scrcpy.exe 的注入链在 GUI 侧的最后一环就是这里——客户端侧读取逻辑
// 由 02_client/tests/overlay_env_test.c 覆盖。
func TestCastEnvParamOverlay(t *testing.T) {
	cases := []struct {
		name   string
		params CastParams
		want   string // "" = 不注入该变量
	}{
		{
			name:   "未设置：不注入（旧调用方/bat 行为不变）",
			params: CastParams{},
			want:   "",
		},
		{
			name:   "开关 A 开：注入 1（启动投屏时显示参数控件）",
			params: CastParams{OverlayVisible: true, OverlayVisibleSet: true},
			want:   "1",
		},
		{
			name:   "开关 A 关：注入 0（启动投屏时隐藏参数控件）",
			params: CastParams{OverlayVisible: false, OverlayVisibleSet: true},
			want:   "0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := envMap(castEnv(tc.params, "tag-x"))
			got, ok := env["SCEZ_PARAM_OVERLAY"]
			if tc.want == "" {
				if ok {
					t.Fatalf("未设置 OverlayVisibleSet 时不应注入 SCEZ_PARAM_OVERLAY（got %q）", got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Fatalf("SCEZ_PARAM_OVERLAY = %q (present=%v)，期望 %q", got, ok, tc.want)
			}
		})
	}
}

// 回归：既有注入项不受新增字段影响（未设置 OverlayVisibleSet 时与历史完全一致）。
func TestCastEnvLegacyFieldsUnchanged(t *testing.T) {
	params := CastParams{
		Usb:    ModeParams{Res: 1920, FPS: 60, Bitrate: 8, Set: true},
		Serial: "24117RK2CC",
		Addr:   "192.0.2.197:5555",
		Addr2:  "192.0.2.197:45005",
		Market: "Redmi K80",
		Model:  "24117RK2CC",
	}
	env := envMap(castEnv(params, "tag-1"))
	want := map[string]string{
		"SCEZ_NO_ADB_RESET": "1",
		"SCEZ_RES_USB":      "1920",
		"SCEZ_FPS_USB":      "60",
		"SCEZ_BITRATE_USB":  "8",
		"SCEZ_SERIAL":       "24117RK2CC",
		"SCEZ_ADDR":         "192.0.2.197:5555",
		"SCEZ_ADDR2":        "192.0.2.197:45005",
		"SCEZ_MARKET":       "Redmi K80",
		"SCEZ_MODEL":        "24117RK2CC",
		"SCEZ_WATCH_TAG":    "tag-1",
	}
	for k, v := range want {
		if env[k] != v {
			t.Fatalf("%s = %q，期望 %q（env=%v）", k, env[k], v, env)
		}
	}
	// 未设置的字段/模式不产生条目（bat 走原逻辑）
	for _, k := range []string{"SCEZ_RES_WIFI", "SCEZ_FPS_WIFI", "SCEZ_BITRATE_WIFI",
		"SCEZ_NO_WATCH", "SCEZ_PARAM_OVERLAY"} {
		if _, ok := env[k]; ok {
			t.Fatalf("未设置字段不应注入 %s（env=%v）", k, env)
		}
	}
}

// 回归：开关 A 与其它注入项共存（真实 GUI 会话总是同时注入 SERIAL/ADDR/...）。
func TestCastEnvParamOverlayCoexists(t *testing.T) {
	params := CastParams{
		Wifi:              ModeParams{Res: 1920, FPS: 60, Bitrate: 12, Set: true},
		Serial:            "192.0.2.197:5555",
		Addr:              "192.0.2.197:5555",
		OverlayVisible:    false,
		OverlayVisibleSet: true,
	}
	env := envMap(castEnv(params, "tag-2"))
	if env["SCEZ_PARAM_OVERLAY"] != "0" {
		t.Fatalf("开关 A 关应注入 0: %v", env)
	}
	if env["SCEZ_RES_WIFI"] != "1920" || env["SCEZ_ADDR"] != "192.0.2.197:5555" {
		t.Fatalf("与开关 A 共存的既有注入项丢失: %v", env)
	}
}
