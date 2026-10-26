package ui

// Nerd Font glyphs, one cell wide each. They are escapes by code point because
// editors silently drop the private-use characters.
const (
	iconAgent  = "\U000F06A9" // nf-md-robot
	iconShell  = "\uF489"     // nf-oct-terminal
	iconZoom   = "\U000F0293" // nf-md-fullscreen
	iconApp    = "\U000F056E" // nf-md-view_dashboard
	iconLeader = "\U000F030C" // nf-md-keyboard
	iconAlert  = "\U000F002A" // nf-md-alert_outline
	iconFolder = "\U000F024B" // nf-md-folder
)

// Powerline edges and dividers.
const (
	edgeSolid = "\uE0BC" // nf-ple-upper_left_triangle
	edgeThin  = "\uE0BB" // nf-ple-forwardslash_separator
	trayOpen  = "\uE0BA" // nf-ple-lower_right_triangle
)
