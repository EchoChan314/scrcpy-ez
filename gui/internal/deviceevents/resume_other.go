//go:build !windows

package deviceevents

func resumeSignals() (<-chan struct{}, func(), error) { return nil, func() {}, nil }
