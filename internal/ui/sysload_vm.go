package ui

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
