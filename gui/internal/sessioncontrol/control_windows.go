//go:build windows

package sessioncontrol

import (
	"fmt"
	"golang.org/x/sys/windows"
)

type Event struct {
	h    windows.Handle
	Name string
}

func StopName(tag string) string { return `Local\SCEZ_STOP_` + tag }

func New(name string) (*Event, error) {
	var p *uint16 // A nil name creates a genuinely unnamed, private event.
	if name != "" {
		var err error
		p, err = windows.UTF16PtrFromString(name)
		if err != nil {
			return nil, err
		}
	}
	h, err := windows.CreateEvent(nil, 1, 0, p)
	if err != nil {
		// x/sys reports ERROR_ALREADY_EXISTS with a valid handle. Do not share
		// another session's state, and do not leak its returned handle.
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil, fmt.Errorf("create event %q: %w", name, err)
	}
	return &Event{h, name}, nil
}
func (e *Event) Signal() error          { return windows.SetEvent(e.h) }
func (e *Event) Handle() windows.Handle { return e.h }
func (e *Event) Close()                 { windows.CloseHandle(e.h) }
func (e *Event) Wait() error {
	n, err := windows.WaitForSingleObject(e.h, windows.INFINITE)
	if err != nil {
		return err
	}
	if n != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("event wait: %d", n)
	}
	return nil
}
func SignalStop(tag string) error {
	p, err := windows.UTF16PtrFromString(StopName(tag))
	if err != nil {
		return err
	}
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, p)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.SetEvent(h)
}
