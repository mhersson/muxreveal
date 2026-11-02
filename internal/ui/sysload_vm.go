package ui

import (
	"encoding/binary"
	"fmt"
)

// vmPages are the macOS page counts the system can reclaim without writing
// anything out. It builds on every platform so its test runs on Linux too.
type vmPages struct {
	free, external, purgeable uint64
}

// used is total minus the reclaimable pages, clamped at zero, so file cache
// does not count as used; it is close to Activity Monitor's "Memory Used".
func (p vmPages) used(total, pageSize uint64) uint64 {
	// Speculative pages are part of the file-backed count, so they are not added again.
	reclaimable := (p.free + p.external + p.purgeable) * pageSize

	return total - min(reclaimable, total)
}

// vmCount decodes a page count sysctl. Its width differs between macOS
// versions, and from one count to another, so 4 and 8 bytes are both read.
func vmCount(name string, raw []byte) (uint64, error) {
	switch len(raw) {
	case 4:
		return uint64(binary.NativeEndian.Uint32(raw)), nil
	case 8:
		return binary.NativeEndian.Uint64(raw), nil
	default:
		return 0, fmt.Errorf("sysctl %s: %d bytes, want 4 or 8", name, len(raw))
	}
}
