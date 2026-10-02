//go:build windows

package sessioncontrol

import (
	"errors"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

func TestNamedCollisionDoesNotShareOrLeakHandle(t *testing.T) {
	tag, err := NewTag("collision-test")
	if err != nil {
		t.Fatal(err)
	}
	name := StopName(tag)
	first, err := New(name)
	if err != nil {
		t.Fatal(err)
	}
	other, err := New(name)
	first.Close()
	if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) || other != nil {
		t.Fatalf("duplicate event must be rejected: event=%v error=%v", other, err)
	}
	// No handle from the rejected creation may keep the old event alive.
	fresh, err := New(name)
	if err != nil {
		t.Fatalf("rejected collision leaked a handle: %v", err)
	}
	fresh.Close()
}

func TestUnnamedControlEventsAreIndependent(t *testing.T) {
	first, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := first.Signal(); err != nil {
		t.Fatal(err)
	}
	state, err := windows.WaitForSingleObject(second.Handle(), 0)
	if err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("private event signal leaked: state=%d err=%v", state, err)
	}
}

func TestConcurrentSessionNamespaces(t *testing.T) {
	const count = 256
	var wg sync.WaitGroup
	gate := make(chan struct{})
	events := make(chan *Event, count)
	errors := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			tag, err := NewTag("batch-test")
			if err != nil {
				errors <- err
				return
			}
			e, err := New(StopName(tag))
			if err != nil {
				errors <- err
				return
			}
			events <- e
		}()
	}
	close(gate)
	wg.Wait()
	close(events)
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	var held []*Event
	for e := range events {
		held = append(held, e)
	}
	defer func() {
		for _, e := range held {
			e.Close()
		}
	}()
	if len(held) != count {
		t.Fatalf("only %d/%d batch namespaces created", len(held), count)
	}
	if err := held[0].Signal(); err != nil {
		t.Fatal(err)
	}
	for _, e := range held[1:] {
		state, err := windows.WaitForSingleObject(e.Handle(), 0)
		if err != nil || state != uint32(windows.WAIT_TIMEOUT) {
			t.Fatalf("stopping one namespace signalled another: %d %v", state, err)
		}
	}
}
