package tiling

import "slices"

const (
	// MasterRatioMin is the smallest master share, in percent.
	MasterRatioMin = 10
	// MasterRatioMax is the largest master share, in percent.
	MasterRatioMax = 90
	// MasterRatioDefault is the default master share, in percent.
	MasterRatioDefault = 50

	// DefaultWindowWidth is the smallest tile width a split may leave.
	DefaultWindowWidth = 20
	// DefaultWindowHeight is the smallest tile height a split may leave.
	DefaultWindowHeight = 5
)

// Master positions: the side of the region the masters occupy.
const (
	// MasterPositionLeft puts the masters in a column on the left.
	MasterPositionLeft = "left"
	// MasterPositionRight puts the masters in a column on the right.
	MasterPositionRight = "right"
	// MasterPositionTop puts the masters in a row on top.
	MasterPositionTop = "top"
	// MasterPositionBottom puts the masters in a row at the bottom.
	MasterPositionBottom = "bottom"
	// MasterPositionCenter puts the masters in a column between two stacks.
	MasterPositionCenter = "center"
)

// MasterPositions lists every master position.
func MasterPositions() []string {
	return []string{
		MasterPositionLeft,
		MasterPositionRight,
		MasterPositionTop,
		MasterPositionBottom,
		MasterPositionCenter,
	}
}

// ValidMasterPosition returns p when it is a master position, else left.
func ValidMasterPosition(p string) string {
	if slices.Contains(MasterPositions(), p) {
		return p
	}
	return MasterPositionLeft
}
