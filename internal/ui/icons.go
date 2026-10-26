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
	iconRename = "\U000F0455" // nf-md-rename_box

	iconPanes     = "\U000F0BCC" // nf-md-view_split_vertical
	iconSplitDown = "\U000F0BCB" // nf-md-view_split_horizontal
	iconTabs      = "\U000F04E9" // nf-md-tab
	iconTabNew    = "\U000F075C" // nf-md-tab_plus
	iconCog       = "\U000F0493" // nf-md-cog
	iconPalette   = "\uF4B5"     // nf-oct-command_palette
	iconTheme     = "\U000F03D8" // nf-md-palette
	iconFocus     = "\U000F04FE" // nf-md-target
	iconResize    = "\U000F0616" // nf-md-arrow_expand
	iconSwap      = "\U000F04E1" // nf-md-swap_horizontal
	iconClose     = "\U000F0156" // nf-md-close
	iconQuit      = "\U000F0206" // nf-md-exit_to_app
)

// Powerline edges and dividers.
const (
	edgeSolid = "\uE0BC" // nf-ple-upper_left_triangle
	edgeThin  = "\uE0BB" // nf-ple-forwardslash_separator
	trayOpen  = "\uE0BA" // nf-ple-lower_right_triangle
)
