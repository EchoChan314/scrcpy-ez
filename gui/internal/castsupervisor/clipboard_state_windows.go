//go:build windows

package castsupervisor

import (
	"crypto/sha256"
	"fmt"

	"golang.org/x/sys/windows"
)

// Hold metadata across client replacement during USB/Wi-Fi handover. The client
// alone interprets the mapping. It contains no clipboard content or addresses.
func retainClipboardState(identity string) (windows.Handle, error) {
	digest := sha256.Sum256(append([]byte("device\x00"), []byte(identity)...))
	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`Local\scrcpy-ez.clipboard.2.2.16.%x`, digest))
	if err != nil {
		return 0, err
	}
	handle, err := windows.CreateFileMapping(windows.InvalidHandle, nil, windows.PAGE_READWRITE, 0, 4096, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		err = nil
	}
	return handle, err
}
