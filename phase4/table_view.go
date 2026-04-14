package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/p2p-poker/internal/game"
)

// TableViewOpts passes rendering hints from the model to the table renderer.
type TableViewOpts struct {
	LocalPlayerID string            // which player is "us" (hole cards shown)
	ActingPlayerID string           // whose turn it is right now
	DealerIdx     int
	WinnerIDs     map[string]bool   // non-nil at showdown
	HandRanks     map[string]string // playerID → hand rank label at showdown
}

// RenderTable builds the complete poker table as a multi-line string.
// Layout (up to 6 players):
//
//	  [p4]      [p5]      [p0]
//	  [p3]   [community]  [p1]
//	           [p2]
//
// For 2–9 players the seats are arranged around the felt oval.
func RenderTable(gs *game.GameState, opts TableViewOpts) string {
	if gs == nil {
		return StyleTable.Render("  no game in progress")
	}

	n := len(gs.Players)

	// ── Info bar ──────────────────────────────────────────────────────────────
	infoBar := renderInfoBar(gs)

	// ── Community area (centre) ───────────────────────────────────────────────
	communityArea := renderCommunityArea(gs)

	// ── Seat layout ───────────────────────────────────────────────────────────
	panels := make([]string, n)
	for i, p := range gs.Players {
		popts := PlayerPanelOpts{
			IsLocalPlayer: p.ID == opts.LocalPlayerID,
			IsActing:      p.ID == opts.ActingPlayerID,
			IsDealer:      i == opts.DealerIdx,
			IsSmallBlind:  i == smallBlindIdx(opts.DealerIdx, n),
			IsBigBlind:    i == bigBlindIdx(opts.DealerIdx, n),
			IsWinner:      opts.WinnerIDs != nil && opts.WinnerIDs[p.ID],
		}
		if opts.HandRanks != nil {
			popts.ShowHandRank = opts.HandRanks[p.ID]
		}
		panels[i] = RenderPlayerPanel(p, popts)
	}

	// Arrange seats into rows depending on player count.
	rows := arrangeSeatRows(panels, n, communityArea)

	// ── Final assembly ────────────────────────────────────────────────────────
	body := lipgloss.JoinVertical(lipgloss.Center, rows...)
	table := StyleTable.Render(lipgloss.JoinVertical(lipgloss.Left, infoBar, body))
	return table
}

// renderInfoBar renders the top bar: hand number, phase, blinds, pot.
func renderInfoBar(gs *game.GameState) string {
	handStr := fmt.Sprintf("Hand #%d", gs.HandNum)
	phaseStr := StylePhase.Render(gs.Phase.String())
	potStr := StylePot.Render(fmt.Sprintf("Pot: $%d", game.TotalPot(gs.Pots)))
	blindStr := fmt.Sprintf("Blinds: $%d/$%d", gs.SmallBlind, gs.BigBlind)

	left := lipgloss.NewStyle().Foreground(lipgloss.Color("#aaaaaa")).Render(
		handStr + "  " + blindStr,
	)
	right := potStr

	gap := TableWidth - lipgloss.Width(left) - lipgloss.Width(phaseStr) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	return StyleInfoBar.Render(
		left + strings.Repeat(" ", gap/2) + phaseStr + strings.Repeat(" ", gap-gap/2) + right,
	)
}

// renderCommunityArea renders the pot display and community cards as a centred block.
func renderCommunityArea(gs *game.GameState) string {
	cards := RenderCommunityCards(gs.CommunityCards)

	// Side pots breakdown.
	var potLines []string
	for i, pot := range gs.Pots {
		label := "Main pot"
		if i > 0 {
			label = fmt.Sprintf("Side pot %d", i)
		}
		potLines = append(potLines, fmt.Sprintf("%s: $%d", label, pot.Amount))
	}

	potInfo := ""
	if len(potLines) > 0 {
		potStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f0c040")).Align(lipgloss.Center)
		potInfo = potStyle.Render(strings.Join(potLines, "  "))
	}

	currentBet := ""
	if gs.CurrentBet > 0 {
		currentBet = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#aaaaaa")).
			Render(fmt.Sprintf("current bet: $%d", gs.CurrentBet))
	}

	lines := []string{cards}
	if potInfo != "" {
		lines = append(lines, potInfo)
	}
	if currentBet != "" {
		lines = append(lines, currentBet)
	}

	inner := lipgloss.JoinVertical(lipgloss.Center, lines...)
	return lipgloss.NewStyle().
		Background(feltGreenDark).
		Padding(1, 4).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#2a6b3c")).
		Align(lipgloss.Center).
		Render(inner)
}

// arrangeSeatRows places player panels around the table based on seat count.
// Returns a slice of rendered rows to be joined vertically.
//
// Seat layout strategy:
//   2 players:  [p0]  [centre]  [p1]
//   3 players:  [p0]  [centre]  [p1]
//               [p2]
//   4–6 players: top row, centre row, bottom row
//   7–9 players: three rows with more seats per row
func arrangeSeatRows(panels []string, n int, centre string) []string {
	pad := lipgloss.NewStyle().Width(4).Render("") // horizontal padding

	switch {
	case n == 2:
		row := lipgloss.JoinHorizontal(lipgloss.Center, panels[0], pad, centre, pad, panels[1])
		return []string{row}

	case n == 3:
		topRow := lipgloss.JoinHorizontal(lipgloss.Center, panels[0], pad, centre, pad, panels[1])
		botRow := centreRow([]string{panels[2]})
		return []string{topRow, botRow}

	case n == 4:
		topRow := lipgloss.JoinHorizontal(lipgloss.Center, panels[3], pad, centre, pad, panels[1])
		botRow := lipgloss.JoinHorizontal(lipgloss.Center, panels[2], pad, pad, pad, panels[0])
		_ = botRow
		midRow := centreRow([]string{panels[2], panels[0]})
		return []string{topRow, midRow}

	case n <= 6:
		// Top row: seats 3, 4, 5 (up to 3 across the top)
		topSeats := panels[3:n]
		topRow := centreRow(topSeats)
		// Middle row: seat 2, centre, seat 0
		midRow := lipgloss.JoinHorizontal(lipgloss.Center, panels[2], pad, centre, pad, panels[0])
		// Bottom row: seat 1
		botRow := centreRow([]string{panels[1]})
		return []string{topRow, midRow, botRow}

	default:
		// 7–9 players: split into thirds.
		third := n / 3
		topSeats := panels[n-third:]
		midLeftSeats := panels[:1]
		midRightSeats := panels[third+1 : third+2]
		botSeats := panels[1 : n-third]

		topRow := centreRow(topSeats)
		midRow := lipgloss.JoinHorizontal(lipgloss.Center,
			centreRow(midLeftSeats), pad, centre, pad, centreRow(midRightSeats))
		botRow := centreRow(botSeats)
		return []string{topRow, midRow, botRow}
	}
}

// centreRow joins a slice of panels horizontally, centred in the table width.
func centreRow(panels []string) string {
	if len(panels) == 0 {
		return ""
	}
	pad := lipgloss.NewStyle().Width(2).Render("")
	parts := []string{panels[0]}
	for _, p := range panels[1:] {
		parts = append(parts, pad, p)
	}
	row := lipgloss.JoinHorizontal(lipgloss.Center, parts...)
	return lipgloss.NewStyle().Width(TableWidth).Align(lipgloss.Center).Render(row)
}

// smallBlindIdx returns the seat index of the small blind.
func smallBlindIdx(dealerIdx, n int) int {
	if n == 2 {
		return dealerIdx // heads-up: dealer posts SB
	}
	return (dealerIdx + 1) % n
}

// bigBlindIdx returns the seat index of the big blind.
func bigBlindIdx(dealerIdx, n int) int {
	if n == 2 {
		return (dealerIdx + 1) % n
	}
	return (dealerIdx + 2) % n
}
