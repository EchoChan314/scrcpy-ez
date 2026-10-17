package adb

import (
	"context"
	"reflect"
	"testing"
)

func TestAppOutputStreamSplitLinesAndBound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var lines []string
	w := &appOutputWriter{limit: 64, cancel: cancel, onLine: func(s string) { lines = append(lines, s) }}
	for _, part := range []string{"INFO: SCEZ_", "APP_PROGRESS:1\r", "\nnext\n"} {
		if _, err := w.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(lines, []string{"INFO: SCEZ_APP_PROGRESS:1", "next"}) {
		t.Fatal(lines)
	}
	if _, err := w.Write(make([]byte, 65)); err == nil || !w.overflow || ctx.Err() == nil || w.all.Len() > 64 {
		t.Fatal("unbounded output")
	}
}
