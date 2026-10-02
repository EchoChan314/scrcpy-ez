//go:build windows

package deviceevents

import (
	"fmt"
	"golang.org/x/sys/windows"
	"sync/atomic"
	"time"
	"unsafe"
)

// Windows supplies real resume notifications; there is no silence watchdog.
// https://learn.microsoft.com/en-us/windows/win32/api/powerbase/nf-powerbase-powerregistersuspendresumenotification
func resumeSignals() (<-chan struct{}, func(), error) {
	dll := windows.NewLazySystemDLL("powrprof.dll")
	register := dll.NewProc("PowerRegisterSuspendResumeNotification")
	unregister := dll.NewProc("PowerUnregisterSuspendResumeNotification")
	if err := register.Find(); err != nil {
		return nil, func() {}, err
	}
	ch := make(chan struct{}, 1)
	var last atomic.Int64
	callback := windows.NewCallback(func(_ uintptr, kind uintptr, _ uintptr) uintptr {
		if kind == 0x12 || kind == 0x07 || kind == 0x06 {
			now := time.Now().UnixNano()
			before := last.Load()
			if now-before > int64(time.Second) && last.CompareAndSwap(before, now) {
				select {
				case ch <- struct{}{}:
				default:
				}
			}
		}
		return 0
	})
	params := struct{ Callback, Context uintptr }{Callback: callback}
	var handle windows.Handle
	code, _, _ := register.Call(2, uintptr(unsafe.Pointer(&params)), uintptr(unsafe.Pointer(&handle)))
	if code != 0 {
		return nil, func() {}, fmt.Errorf("power notification: %d", code)
	}
	return ch, func() { unregister.Call(uintptr(handle)) }, nil
}
