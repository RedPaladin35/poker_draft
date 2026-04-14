package tui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/p2p-poker/internal/game"
)

// BetAction represents the player's chosen action from the input widget.
type BetAction struct {
	Type   game.ActionType
	Amount int64 // only set for Raise
}

// BetInputState holds the mutable state of the betting widget.
type BetInputState struct {
	// Context from game state.
	ToCall   int64  // how much the player needs to call
	MinRaise int64  // minimum legal raise increment
	Stack    int64  // player's current stack
	CanCheck bool   // true when no bet to call

	// Widget state.
	Selected    int    // 0=fold, 1=check/call, 2=raise, 3=all-in
	RaiseInput  string // raw text in the raise amount field
	InputActive bool   // true when typing a raise amount
	Submitted   *BetAction // non-nil when player confirmed
}

// NewBetInputState creates a fresh widget state from the current game context.
func NewBetInputState(p *game.Player, gs *game.GameState) BetInputState {
	toCall := gs.CurrentBet - p.CurrentBet
	if toCall < 0 {
		toCall = 0
	}
	return BetInputState{
		ToCall:   toCall,
		MinRaise: gs.MinRaise,
		Stack:    p.Stack,
		CanCheck: toCall == 0,
		Selected: 1, // default to check/call
	}
}

// RenderBetInput renders the full betting panel.
// Returns a multi-line string showing action buttons and optional raise input.
func RenderBetInput(s BetInputState) string {
	buttons := renderActionButtons(s)
	var raiseRow string
	if s.Selected == 2 { // raise selected
		raiseRow = renderRaiseInput(s)
	}
	hint := renderHint(s)

	var lines []string
	lines = append(lines, buttons)
	if raiseRow != "" {
		lines = append(lines, raiseRow)
	}
	lines = append(lines, hint)

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return StyleBetPanel.Render(content)
}

// renderActionButtons renders the four action buttons in a horizontal row.
func renderActionButtons(s BetInputState) string {
	type btn struct {
		label string
		idx   int
		style lipgloss.Style
	}

	// Build label for check/call button dynamically.
	callLabel := "Call"
	if s.CanCheck {
		callLabel = "Check"
	} else if s.ToCall > 0 {
		callLabel = fmt.Sprintf("Call $%d", s.ToCall)
	}

	buttons := []btn{
		{"Fold", 0, StyleBetButtonDanger},
		{callLabel, 1, StyleBetButton},
		{fmt.Sprintf("Raise"), 2, StyleBetButton},
		{"All-In", 3, StyleBetButton},
	}

	rendered := make([]string, len(buttons))
	for i, b := range buttons {
		style := b.style
		if s.Selected == b.idx {
			style = StyleBetButtonSelected
		}
		rendered[i] = style.Render(b.label)
	}

	return lipgloss.JoinHorizontal(lipgloss.Center, rendered...)
}

// renderRaiseInput renders the raise amount text field.
func renderRaiseInput(s BetInputState) string {
	inputStyle := StyleBetInput
	if s.InputActive {
		inputStyle = inputStyle.BorderForeground(lipgloss.Color("#f0c040"))
	}

	cursor := ""
	if s.InputActive {
		cursor = "█"
	}

	displayVal := s.RaiseInput
	if displayVal == "" {
		displayVal = fmt.Sprintf("%d", s.MinRaise)
	}

	inputWidget := inputStyle.Render(displayVal + cursor)

	label := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#aaaaaa")).
		Render(fmt.Sprintf("Raise by: (min $%d)", s.MinRaise))

	return lipgloss.JoinHorizontal(lipgloss.Left, label, "  ", inputWidget)
}

// renderHint renders keyboard shortcut hints.
func renderHint(s BetInputState) string {
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))
	if s.InputActive {
		return hintStyle.Render("type amount · Enter to confirm · Esc to cancel")
	}
	return hintStyle.Render("←/→ or h/l select · Enter confirm · r raise · a all-in · f fold")
}

// ── BetInputState update methods ──────────────────────────────────────────────
// These are called from the bubbletea Update function.

// SelectNext moves selection right (wraps).
func (s *BetInputState) SelectNext() {
	s.Selected = (s.Selected + 1) % 4
	if s.Selected == 2 {
		s.InputActive = false
	}
}

// SelectPrev moves selection left (wraps).
func (s *BetInputState) SelectPrev() {
	s.Selected = (s.Selected + 3) % 4
	if s.Selected == 2 {
		s.InputActive = false
	}
}

// ActivateInput begins text entry for the raise amount.
func (s *BetInputState) ActivateInput() {
	s.Selected = 2
	s.InputActive = true
	if s.RaiseInput == "" {
		s.RaiseInput = fmt.Sprintf("%d", s.MinRaise)
	}
}

// AppendChar adds a digit to the raise amount field.
func (s *BetInputState) AppendChar(r rune) {
	if unicode.IsDigit(r) && len(s.RaiseInput) < 10 {
		s.RaiseInput += string(r)
	}
}

// Backspace removes the last character from the raise amount.
func (s *BetInputState) Backspace() {
	if len(s.RaiseInput) > 0 {
		s.RaiseInput = s.RaiseInput[:len(s.RaiseInput)-1]
	}
}

// Confirm validates and returns the BetAction for the current selection.
// Returns nil and an error string if the action is invalid.
func (s *BetInputState) Confirm() (*BetAction, string) {
	switch s.Selected {
	case 0:
		return &BetAction{Type: game.ActionFold}, ""

	case 1:
		if s.CanCheck {
			return &BetAction{Type: game.ActionCheck}, ""
		}
		return &BetAction{Type: game.ActionCall}, ""

	case 2:
		raw := strings.TrimSpace(s.RaiseInput)
		if raw == "" {
			raw = fmt.Sprintf("%d", s.MinRaise)
		}
		amount, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || amount <= 0 {
			return nil, "invalid raise amount"
		}
		if amount < s.MinRaise {
			return nil, fmt.Sprintf("raise must be at least $%d", s.MinRaise)
		}
		if amount+s.ToCall > s.Stack {
			return nil, "not enough chips"
		}
		return &BetAction{Type: game.ActionRaise, Amount: amount}, ""

	case 3:
		return &BetAction{Type: game.ActionAllIn}, ""
	}
	return nil, "unknown selection"
}
