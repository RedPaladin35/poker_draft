package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/p2p-poker/internal/game"
)

// ── Message types sent to the bubbletea Update loop ──────────────────────────

// GameStateMsg carries a new GameState snapshot from the game engine or network.
type GameStateMsg struct {
	State *game.GameState
}

// ActionResultMsg carries the result of submitting a player action.
type ActionResultMsg struct {
	Err string // empty = success
}

// WinnerMsg is sent at showdown with winner info and hand ranks.
type WinnerMsg struct {
	WinnerIDs map[string]bool
	HandRanks map[string]string // playerID → "Full House, Aces full of Kings"
	Payouts   map[string]int64
}

// NetworkMsg carries a network event for the log.
type NetworkMsg struct {
	Text string
}

// ErrorMsg carries an error message to display.
type ErrorMsg struct {
	Text string
}

// ── TUI modes ─────────────────────────────────────────────────────────────────

// UIMode controls which overlay is shown.
type UIMode uint8

const (
	ModeSpectate UIMode = iota // watching, not our turn
	ModeBetting                // our turn — bet input widget active
	ModeShowdown               // hand over, showing results
	ModeLobby                  // waiting for players to join
	ModeError                  // fatal error overlay
)

// ── Model ─────────────────────────────────────────────────────────────────────

// Model is the root bubbletea model for the poker TUI.
// It implements tea.Model (Init, Update, View).
type Model struct {
	// Game state (updated via GameStateMsg).
	GameState     *game.GameState
	LocalPlayerID string

	// UI state.
	Mode      UIMode
	BetInput  BetInputState
	Log       *LogView
	WinnerIDs map[string]bool
	HandRanks map[string]string

	// Error overlay text.
	ErrorText string

	// Lobby status line.
	LobbyStatus string

	// Dimensions (set by WindowSizeMsg).
	Width  int
	Height int

	// Callback: called when the local player submits an action.
	// In production this sends the action over the network.
	OnAction func(game.Action)
}

// NewModel creates a new TUI model for the given local player.
func NewModel(localPlayerID string, onAction func(game.Action)) Model {
	return Model{
		LocalPlayerID: localPlayerID,
		Mode:          ModeLobby,
		Log:           NewLogView(),
		OnAction:      onAction,
		Width:         TableWidth,
		Height:        TableHeight + LogHeight + 4,
	}
}

// ── tea.Model implementation ──────────────────────────────────────────────────

// Init starts the bubbletea program — no initial command needed.
func (m Model) Init() tea.Cmd {
	return nil
}

// Update handles all incoming messages and key presses.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	// ── Window resize ─────────────────────────────────────────────────────────
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
		return m, nil

	// ── Quit ──────────────────────────────────────────────────────────────────
	case tea.KeyMsg:
		// Global quit.
		if msg.String() == "ctrl+c" || msg.String() == "q" && m.Mode != ModeBetting {
			return m, tea.Quit
		}
		return m.handleKey(msg)

	// ── Game state update ─────────────────────────────────────────────────────
	case GameStateMsg:
		prev := m.GameState
		m.GameState = msg.State

		// Log phase transitions.
		if prev == nil || prev.Phase != msg.State.Phase {
			m.Log.AddPhase(msg.State.Phase)
		}

		// Update mode.
		switch msg.State.Phase {
		case game.PhaseSettled:
			m.Mode = ModeShowdown
		case game.PhaseWaiting:
			m.Mode = ModeLobby
		default:
			current := msg.State.CurrentPlayer()
			if current != nil && current.ID == m.LocalPlayerID {
				m.Mode = ModeBetting
				m.BetInput = NewBetInputState(current, msg.State)
			} else {
				m.Mode = ModeSpectate
			}
		}
		return m, nil

	// ── Action result ─────────────────────────────────────────────────────────
	case ActionResultMsg:
		if msg.Err != "" {
			m.Log.AddError(msg.Err)
			m.BetInput.Submitted = nil
		} else {
			m.Mode = ModeSpectate
		}
		return m, nil

	// ── Showdown / winner ─────────────────────────────────────────────────────
	case WinnerMsg:
		m.WinnerIDs = msg.WinnerIDs
		m.HandRanks = msg.HandRanks
		m.Mode = ModeShowdown
		for id, payout := range msg.Payouts {
			if payout > 0 {
				name := m.playerName(id)
				rank := msg.HandRanks[id]
				m.Log.AddWinner(name, payout, rank)
			}
		}
		return m, nil

	// ── Network events ────────────────────────────────────────────────────────
	case NetworkMsg:
		m.Log.AddNetwork(msg.Text)
		return m, nil

	// ── Error ─────────────────────────────────────────────────────────────────
	case ErrorMsg:
		m.ErrorText = msg.Text
		m.Mode = ModeError
		m.Log.AddError(msg.Text)
		return m, nil
	}

	return m, nil
}

// handleKey routes key presses based on current mode.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.Mode {

	case ModeBetting:
		return m.handleBettingKey(msg)

	case ModeShowdown, ModeSpectate:
		// Log scrolling.
		switch msg.String() {
		case "up", "k":
			m.Log.ScrollUp()
		case "down", "j":
			m.Log.ScrollDown()
		}

	case ModeLobby:
		// Nothing interactive in lobby — just spectate.

	case ModeError:
		if msg.String() == "enter" || msg.String() == "esc" {
			m.Mode = ModeSpectate
			m.ErrorText = ""
		}
	}

	return m, nil
}

// handleBettingKey handles keypresses during the betting phase.
func (m Model) handleBettingKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.BetInput.InputActive {
		// Typing mode: capture digits, backspace, enter, escape.
		switch msg.String() {
		case "enter":
			m.BetInput.InputActive = false
			return m.submitAction()
		case "esc":
			m.BetInput.InputActive = false
			m.BetInput.RaiseInput = ""
		case "backspace":
			m.BetInput.Backspace()
		default:
			if len(msg.String()) == 1 {
				m.BetInput.AppendChar(rune(msg.String()[0]))
			}
		}
		return m, nil
	}

	// Normal mode: navigate buttons.
	switch msg.String() {
	case "right", "l", "tab":
		m.BetInput.SelectNext()
	case "left", "h", "shift+tab":
		m.BetInput.SelectPrev()
	case "f":
		m.BetInput.Selected = 0 // fold
	case "c":
		m.BetInput.Selected = 1 // check/call
	case "r":
		m.BetInput.ActivateInput()
	case "a":
		m.BetInput.Selected = 3 // all-in
	case "enter", " ":
		if m.BetInput.Selected == 2 && !m.BetInput.InputActive {
			m.BetInput.ActivateInput()
		} else {
			return m.submitAction()
		}
	case "up", "k":
		m.Log.ScrollUp()
	case "down", "j":
		m.Log.ScrollDown()
	}

	return m, nil
}

// submitAction validates the bet input and fires the OnAction callback.
func (m Model) submitAction() (tea.Model, tea.Cmd) {
	betAction, errStr := m.BetInput.Confirm()
	if errStr != "" {
		m.Log.AddError(errStr)
		return m, nil
	}

	a := game.Action{
		PlayerID: m.LocalPlayerID,
		Type:     betAction.Type,
		Amount:   betAction.Amount,
	}

	// Log our own action immediately (before network round-trip confirms it).
	m.Log.AddAction(m.playerName(m.LocalPlayerID), a)

	if m.OnAction != nil {
		m.OnAction(a)
	}

	m.Mode = ModeSpectate
	return m, nil
}

// ── View ──────────────────────────────────────────────────────────────────────

// View renders the complete TUI as a string.
func (m Model) View() string {
	switch m.Mode {
	case ModeLobby:
		return m.viewLobby()
	case ModeError:
		return m.viewError()
	}

	// Normal game view: table + optional betting widget + log.
	tableOpts := TableViewOpts{
		LocalPlayerID:  m.LocalPlayerID,
		WinnerIDs:      m.WinnerIDs,
		HandRanks:      m.HandRanks,
	}
	if m.GameState != nil {
		tableOpts.DealerIdx = m.GameState.DealerIdx
		current := m.GameState.CurrentPlayer()
		if current != nil {
			tableOpts.ActingPlayerID = current.ID
		}
	}

	table := RenderTable(m.GameState, tableOpts)

	var overlay string
	switch m.Mode {
	case ModeBetting:
		overlay = "\n" + centreInWidth(RenderBetInput(m.BetInput), m.Width)
	case ModeShowdown:
		overlay = "\n" + centreInWidth(m.renderShowdownBanner(), m.Width)
	}

	logPanel := m.Log.Render()
	keybinds := m.renderKeybinds()

	return lipgloss.JoinVertical(lipgloss.Left,
		table,
		overlay,
		logPanel,
		keybinds,
	)
}

// viewLobby renders the waiting-for-players screen.
func (m Model) viewLobby() string {
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#f0c040")).
		Align(lipgloss.Center).
		Width(TableWidth).
		Render("♠ ♥  P2P POKER  ♦ ♣")

	status := m.LobbyStatus
	if status == "" {
		status = "Waiting for players to join..."
	}

	statusLine := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#aaaaaa")).
		Align(lipgloss.Center).
		Width(TableWidth).
		Render(status)

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#555555")).
		Align(lipgloss.Center).
		Width(TableWidth).
		Render("ctrl+c to quit")

	felt := lipgloss.NewStyle().
		Background(feltGreen).
		Width(TableWidth).
		Height(TableHeight).
		Padding(TableHeight/3, 0)

	inner := lipgloss.JoinVertical(lipgloss.Center, title, "", statusLine, "", hint)
	return felt.Render(inner) + "\n" + m.Log.Render()
}

// viewError renders the error overlay.
func (m Model) viewError() string {
	box := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("#e05050")).
		Padding(1, 3).
		Render(
			lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e05050")).Render("ERROR") +
				"\n\n" + m.ErrorText + "\n\nPress Enter to continue",
		)
	return centreInWidth(box, m.Width) + "\n" + m.Log.Render()
}

// renderShowdownBanner renders the showdown result summary.
func (m Model) renderShowdownBanner() string {
	if m.WinnerIDs == nil || m.GameState == nil {
		return ""
	}

	var winners []string
	for id := range m.WinnerIDs {
		name := m.playerName(id)
		if rank, ok := m.HandRanks[id]; ok && rank != "" {
			winners = append(winners, fmt.Sprintf("%s  (%s)", name, rank))
		} else {
			winners = append(winners, name)
		}
	}

	text := "🏆  " + strings.Join(winners, "  •  ")
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#f0c040")).
		Background(feltGreenDark).
		Padding(0, 3).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#f0c040")).
		Render(text)
}

// renderKeybinds renders a one-line keyboard hint bar.
func (m Model) renderKeybinds() string {
	var hints string
	switch m.Mode {
	case ModeBetting:
		hints = "f fold  c check/call  r raise  a all-in  ←/→ select  Enter confirm  q quit"
	case ModeShowdown:
		hints = "↑/↓ scroll log  q quit"
	default:
		hints = "↑/↓ scroll log  q quit"
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#444444")).
		Background(lipgloss.Color("#111111")).
		Width(m.Width).
		Padding(0, 1).
		Render(hints)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// playerName returns a player's display name from the current GameState, or
// falls back to the raw ID if not found.
func (m Model) playerName(playerID string) string {
	if m.GameState == nil {
		return playerID
	}
	for _, p := range m.GameState.Players {
		if p.ID == playerID {
			return p.Name
		}
	}
	return playerID
}

// centreInWidth centres content horizontally within a given terminal width.
func centreInWidth(content string, width int) string {
	return lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(content)
}
