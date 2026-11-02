package ui

import "testing"

func TestVMPagesUsed(t *testing.T) {
	t.Parallel()

	const mib = 1 << 20 // 256 pages of 4 KiB

	tests := []struct {
		name  string
		pages vmPages
		total uint64
		page  uint64
		want  uint64
	}{
		{"nothing reclaimable", vmPages{}, mib, 4096, mib},
		{"free", vmPages{free: 10}, mib, 4096, 246 * 4096},
		{"file-backed", vmPages{external: 30}, mib, 4096, 226 * 4096},
		{"purgeable", vmPages{purgeable: 40}, mib, 4096, 216 * 4096},
		{"all reclaimable", vmPages{free: 100, external: 110, purgeable: 46}, mib, 4096, 0},
		{"more than the total", vmPages{free: 300}, mib, 4096, 0},
		// 16 GiB of 16 KiB pages is 1048576 pages; 639019 are reclaimable.
		{"16 KiB pages", vmPages{free: 74010, external: 527812, purgeable: 37197}, 16 << 30, 16384, 409557 * 16384},
		// A 36 GiB Mac's vm_stat: 2359296 pages, 630199 reclaimable, about 26.4 GiB used.
		{"36 GiB Mac", vmPages{free: 42591, external: 554355, purgeable: 33253}, 36 << 30, 16384, 1729097 * 16384},
	}

	for _, tt := range tests {
		if got := tt.pages.used(tt.total, tt.page); got != tt.want {
			t.Errorf("%s: used = %d, want %d", tt.name, got, tt.want)
		}
	}
}
