//go:build windows

package castsupervisor

import (
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestClipboardMetadataSurvivesClientReplacement(t *testing.T) {
	identity := fmt.Sprintf("test-clipboard-%d", os.Getpid())
	supervisor, err := retainClipboardState(identity)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(supervisor)
	client, err := retainClipboardState(identity)
	if err != nil {
		t.Fatal(err)
	}
	view, err := windows.MapViewOfFile(client, windows.FILE_MAP_READ|windows.FILE_MAP_WRITE, 0, 0, 4096)
	if err != nil {
		windows.CloseHandle(client)
		t.Fatal(err)
	}
	value := byte(42)
	if err := windows.WriteProcessMemory(windows.CurrentProcess(), view+512, &value, 1, nil); err != nil {
		t.Fatal(err)
	}
	windows.UnmapViewOfFile(view)
	windows.CloseHandle(client)
	replacement, err := retainClipboardState(identity)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(replacement)
	view, err = windows.MapViewOfFile(replacement, windows.FILE_MAP_READ, 0, 0, 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.UnmapViewOfFile(view)
	if err := windows.ReadProcessMemory(windows.CurrentProcess(), view+512, &value, 1, nil); err != nil {
		t.Fatal(err)
	}
	if value != 42 {
		t.Fatal("clipboard metadata was lost when the previous client exited")
	}
}
