package tui

import "github.com/charmbracelet/lipgloss"

// ── Layout constants ──────────────────────────────────────────────────────────

const (
	TableWidth   = 100 // total terminal columns the table occupies
	TableHeight  = 30  // total terminal rows
	LogHeight    = 8   // rows reserved for the action log
	PlayerWidth  = 22  // width of each player panel
	CardWidth    = 4   // width of a single rendered card glyph ("A♠ ")
)

// ── Colour palette ────────────────────────────────────────────────────────────

var (
	// Felt green — the poker table surface.
	feltGreen     = lipgloss.Color("#1a6b3c")
	feltGreenDark = lipgloss.Color("#134d2c")

	// Card colours.
	colorRed   = lipgloss.Color("#e03c3c")
	colorBlack = lipgloss.Color("#e8e8e8")
	colorBack  = lipgloss.Color("#3a5f8a") // card back (face-down)

	// UI chrome.
	colorGold    = lipgloss.Color("#f0c040")
	colorSilver  = lipgloss.Color("#aaaaaa")
	colorMuted   = lipgloss.Color("#666666")
	colorWhite   = lipgloss.Color("#f0f0f0")
	colorDanger  = lipgloss.Color("#e05050")
	colorSuccess = lipgloss.Color("#50c050")
	colorInfo    = lipgloss.Color("#5090e0")

	// Status badge colours.
	colorActive    = lipgloss.Color("#50c050")
	colorFolded    = lipgloss.Color("#666666")
	colorAllIn     = lipgloss.Color("#f0a030")
	colorSittingOut = lipgloss.Color("#884444")
	colorActing    = lipgloss.Color("#f0c040")
)

// ── Base styles ───────────────────────────────────────────────────────────────

var (
	// Table surface.
	StyleTable = lipgloss.NewStyle().
			Background(feltGreen).
			Width(TableWidth)

	// Panel used for each player seat.
	StylePlayerPanel = lipgloss.NewStyle().
				Width(PlayerWidth).
				Padding(0, 1).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorMuted)

	// Active player panel (it's their turn).
	StylePlayerPanelActing = StylePlayerPanel.
				BorderForeground(colorActing).
				BorderStyle(lipgloss.DoubleBorder())

	// Folded player panel (dimmed).
	StylePlayerPanelFolded = StylePlayerPanel.
				BorderForeground(colorFolded)

	// ── Text styles ───────────────────────────────────────────────────────────

	StylePlayerName = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWhite).
			MaxWidth(PlayerWidth - 4)

	StylePlayerStack = lipgloss.NewStyle().
				Foreground(colorGold)

	StylePlayerNameMuted = StylePlayerName.
				Foreground(colorMuted)

	StylePot = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorGold).
			Background(feltGreenDark).
			Padding(0, 2)

	StylePhase = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWhite).
			Background(feltGreenDark).
			Padding(0, 1)

	StyleDealerChip = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#000000")).
			Background(colorWhite).
			Padding(0, 1)

	StyleSmallBlindLabel = lipgloss.NewStyle().
				Foreground(colorSilver).
				Background(lipgloss.Color("#445566")).
				Padding(0, 1)

	StyleBigBlindLabel = StyleSmallBlindLabel.
				Background(lipgloss.Color("#665544"))

	// ── Card styles ───────────────────────────────────────────────────────────

	// Face-up card with a red suit (♥ ♦).
	StyleCardRed = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorRed).
			Background(colorWhite).
			Padding(0, 1)

	// Face-up card with a black suit (♠ ♣).
	StyleCardBlack = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#111111")).
			Background(colorWhite).
			Padding(0, 1)

	// Face-down card back.
	StyleCardBack = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWhite).
			Background(colorBack).
			Padding(0, 1)

	// Community card wrapper (adds spacing between cards).
	StyleCardRow = lipgloss.NewStyle().
			Padding(0, 1)

	// ── Status badge styles ───────────────────────────────────────────────────

	StyleBadgeActive = lipgloss.NewStyle().
				Foreground(colorActive).
				Bold(true)

	StyleBadgeFolded = lipgloss.NewStyle().
				Foreground(colorFolded)

	StyleBadgeAllIn = lipgloss.NewStyle().
				Foreground(colorAllIn).
				Bold(true)

	StyleBadgeSittingOut = lipgloss.NewStyle().
				Foreground(colorSittingOut)

	StyleBadgeActing = lipgloss.NewStyle().
				Foreground(colorActing).
				Bold(true)

	// ── Bet input styles ──────────────────────────────────────────────────────

	StyleBetPanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorGold).
			Padding(0, 2).
			Width(60)

	StyleBetButton = lipgloss.NewStyle().
			Foreground(colorWhite).
			Background(lipgloss.Color("#336633")).
			Padding(0, 2).
			MarginRight(1)

	StyleBetButtonSelected = StyleBetButton.
				Background(colorGold).
				Foreground(lipgloss.Color("#000000"))

	StyleBetButtonDanger = StyleBetButton.
				Background(lipgloss.Color("#662222"))

	StyleBetInput = lipgloss.NewStyle().
			Foreground(colorGold).
			Border(lipgloss.NormalBorder()).
			BorderForeground(colorGold).
			Padding(0, 1).
			Width(12)

	// ── Log styles ────────────────────────────────────────────────────────────

	StyleLogPanel = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(colorMuted).
			Height(LogHeight).
			Width(TableWidth).
			Padding(0, 1)

	StyleLogEntry = lipgloss.NewStyle().
			Foreground(colorSilver)

	StyleLogEntryHighlight = lipgloss.NewStyle().
				Foreground(colorWhite)

	StyleLogEntryAction = lipgloss.NewStyle().
				Foreground(colorGold)

	StyleLogEntryWinner = lipgloss.NewStyle().
				Foreground(colorSuccess).
				Bold(true)

	StyleLogEntryError = lipgloss.NewStyle().
				Foreground(colorDanger)

	// ── Showdown / winner highlight ───────────────────────────────────────────

	StyleWinnerPanel = StylePlayerPanel.
				BorderForeground(colorGold).
				BorderStyle(lipgloss.DoubleBorder())

	StyleHandRank = lipgloss.NewStyle().
			Foreground(colorGold).
			Italic(true)

	// ── Info bar (top of screen) ──────────────────────────────────────────────

	StyleInfoBar = lipgloss.NewStyle().
			Background(feltGreenDark).
			Foreground(colorSilver).
			Width(TableWidth).
			Padding(0, 1)
)

// SuitColor returns the lipgloss foreground color for a given suit symbol.
func SuitColor(suit string) lipgloss.Color {
	switch suit {
	case "♥", "♦":
		return colorRed
	default:
		return lipgloss.Color("#111111")
	}
}
