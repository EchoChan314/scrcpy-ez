//go:build windows

package castsupervisor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Compile and exercise the actual native monitor, with an SDL event stub.
func TestNativeClientSessionControl(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "app", "src", "scrcpy.c"))
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	start := strings.Index(s, "struct session_monitor {")
	end := strings.Index(s[start:], "#endif") + start
	if start < 0 || end < start {
		t.Fatal("missing native monitor")
	}
	header := `#include <windows.h>
#include <stdio.h>
#include <stdint.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>
#define SC_EVENT_ROUTE_SWITCH 42
#define SDL_EVENT_QUIT 17
#define LOGE(...) fprintf(stderr, __VA_ARGS__)
static HANDLE delivered;
static int observed;
static void sc_push_event(int event) { observed=event; SetEvent(delivered); }
`
	main := `
int main(int argc, char **argv) {
    if (argc>1 && !strcmp(argv[1],"child")) { Sleep(30000); return 0; }
    char name[128], pid[24];
    snprintf(name,sizeof(name),"Local\\SCEZ_NATIVE_STOP_%lu",GetCurrentProcessId());
    HANDLE stop=CreateEventA(NULL,TRUE,FALSE,name); _putenv_s("SCEZ_EVENT_STOP_NAME",name);
    snprintf(name,sizeof(name),"Local\\SCEZ_NATIVE_ROUTE_%lu",GetCurrentProcessId());
    HANDLE route=CreateEventA(NULL,TRUE,FALSE,name); _putenv_s("SCEZ_EVENT_SWITCH_NAME",name);
    snprintf(pid,sizeof(pid),"%lu",GetCurrentProcessId()); _putenv_s("SCEZ_EVENT_SUPERVISOR_PID",pid);
    delivered=CreateEventA(NULL,TRUE,FALSE,NULL);
    struct session_monitor m;
    for (int i=0;i<3;i++) {
        observed=0; ResetEvent(delivered); ResetEvent(stop); ResetEvent(route);
        session_monitor_start(&m);
        if (!m.thread) return 10;
        if (i==0) SetEvent(route); else if (i==1) SetEvent(stop);
        if (i<2 && WaitForSingleObject(delivered,2000)!=WAIT_OBJECT_0) return 11;
        session_monitor_finish(&m);
        if (observed!=(i==0?42:i==1?17:0)) return 12;
    }
    STARTUPINFOA si={.cb=sizeof(si)}; PROCESS_INFORMATION pi;
    char command[2048]; snprintf(command,sizeof(command),"\"%s\" child",argv[0]);
    if (!CreateProcessA(NULL,command,NULL,NULL,FALSE,CREATE_NO_WINDOW,NULL,NULL,&si,&pi)) return 13;
    snprintf(pid,sizeof(pid),"%lu",pi.dwProcessId); _putenv_s("SCEZ_EVENT_SUPERVISOR_PID",pid);
    observed=0; ResetEvent(delivered); ResetEvent(stop); ResetEvent(route);
    session_monitor_start(&m); TerminateProcess(pi.hProcess,1);
    if (WaitForSingleObject(delivered,2000)!=WAIT_OBJECT_0 || observed!=17) return 14;
    session_monitor_finish(&m); CloseHandle(pi.hThread); CloseHandle(pi.hProcess);
    CloseHandle(stop); CloseHandle(route); CloseHandle(delivered);
    return 0;
}`
	dir := t.TempDir()
	src := filepath.Join(dir, "monitor.c")
	exe := filepath.Join(dir, "monitor.exe")
	if err = os.WriteFile(src, []byte(header+s[start:end]+main), 0600); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("gcc", src, "-O2", "-o", exe)
	hide(c)
	if b, err := c.CombinedOutput(); err != nil {
		t.Fatal(fmt.Sprintf("native compile: %v %s", err, b))
	}
	c = exec.Command(exe)
	hide(c)
	if b, err := c.CombinedOutput(); err != nil {
		t.Fatalf("native control: %v %s", err, b)
	}
}
