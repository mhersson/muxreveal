//go:build !linux && !darwin

package ui

// newSysLoad has no sampler on this platform, so the tray shows no load module.
func newSysLoad() loadSampler { return nil }
