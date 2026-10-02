//go:build windows

package bridge

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestBatReadLoopWritesUTF8DebugLog(t *testing.T) {
	DisableDebugLog()
	path := filepath.Join(t.TempDir(), "bridge.log")
	EnableDebugLog(path)
	t.Cleanup(DisableDebugLog)
	text := "[提示] 本次连接连续失败，等待设备状态变化或手动重投\r\n"
	raw, err := simplifiedchinese.GBK.NewEncoder().String(text)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	r := &BatRunner{onLine: func(line string) { got = line }}
	r.readLoop(io.NopCloser(strings.NewReader(raw)), "out")
	DisableDebugLog()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != text || !utf8.Valid(b) || !strings.Contains(string(b), strings.TrimSpace(text)) {
		t.Fatalf("mixed log encoding: callback=%q log=%q", got, b)
	}
}
