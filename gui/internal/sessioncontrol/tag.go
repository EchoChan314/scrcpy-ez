package sessioncontrol

import (
	"crypto/rand"
	"encoding/hex"
)

// NewTag gives every session its own kernel-event and bootstrap-marker namespace.
// Clock readings can repeat when several batch starts happen together on Windows.
func NewTag(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(b[:]), nil
}
