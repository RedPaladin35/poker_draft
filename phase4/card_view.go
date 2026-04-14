package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/p2p-poker/internal/game"
)

// RenderCard renders a single face-up card with suit-appropriate colouring.
// Red suits (♥ ♦) render with a red foreground; black suits (♠ ♣) with near-black.
// Example output: "A♠" on a white background.
func RenderCard(c game.Card) string {
	text := fmt.Sprintf("%s%s", c.Rank, c.Suit)
	switch c.Suit {
	case game.Hearts, game.Diamonds:
		return StyleCardRed.Render(text)
	default:
		return StyleCardBlack.Render(text)
	}
}

// RenderCardBack renders a face-down card (hidden).
// Used for opponent hole cards before showdown.
func RenderCardBack() string {
	return StyleCardBack.Render("??")
}

// RenderCardPlaceholder renders an empty card slot (for undealt community cards).
func RenderCardPlaceholder() string {
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#2a5530")).
		Background(feltGreen).
		Padding(0, 1)
	return style.Render("  ")
}

// RenderHoleCards renders a player's two hole cards.
// If reveal is true, both cards are shown face-up.
// If reveal is false, both are shown as backs (used for opponents).
// If a card is the zero value it renders as a placeholder.
func RenderHoleCards(cards [2]game.Card, reveal bool) string {
	zero := game.Card{}
	if cards[0] == zero && cards[1] == zero {
		// No cards dealt yet.
		return RenderCardPlaceholder() + " " + RenderCardPlaceholder()
	}
	if !reveal {
		return RenderCardBack() + " " + RenderCardBack()
	}
	return RenderCard(cards[0]) + " " + RenderCard(cards[1])
}

// RenderCommunityCards renders the board (0–5 community cards).
// Empty slots for undealt cards are shown as dim placeholders.
func RenderCommunityCards(cards []game.Card) string {
	slots := make([]string, 5)
	for i := range slots {
		if i < len(cards) {
			slots[i] = RenderCard(cards[i])
		} else {
			slots[i] = RenderCardPlaceholder()
		}
	}
	// Add a visual separator between flop and turn/river.
	flop := lipgloss.JoinHorizontal(lipgloss.Center, slots[0], " ", slots[1], " ", slots[2])
	turnRiver := lipgloss.JoinHorizontal(lipgloss.Center, slots[3], " ", slots[4])
	gap := lipgloss.NewStyle().Foreground(lipgloss.Color("#2a5530")).Render("  ")
	return lipgloss.JoinHorizontal(lipgloss.Center, flop, gap, turnRiver)
}

// RenderWinningHand renders the five-card winning hand with card styling.
func RenderWinningHand(cards [5]game.Card) string {
	rendered := make([]string, 5)
	for i, c := range cards {
		rendered[i] = RenderCard(c)
	}
	return lipgloss.JoinHorizontal(lipgloss.Center,
		rendered[0], " ", rendered[1], " ", rendered[2], " ", rendered[3], " ", rendered[4])
}
