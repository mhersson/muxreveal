// Ported from tuios internal/layout/export_test.go at commit
// aaaa128cd35b38ba8aa08a5f69e688f01385ec3e (github.com/Gaurav-Gosain/tuios).
// Copyright (c) 2025 Gaurav Gosain. MIT License; see THIRD_PARTY.md.

package tiling

// Seams the tests reach for. Production code calls the functions these wrap.

// CalculateTilingLayout is CalculateMasterStackLayout with the two stacked
// panes of the three pane layout sharing the height equally.
func CalculateTilingLayout(n int, screenWidth int, usableHeight int, topMargin int, masterRatio float64, gap int) []TileLayout {
	return CalculateMasterStackLayout(n, screenWidth, usableHeight, topMargin, masterRatio, 0, gap)
}
