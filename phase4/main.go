package main

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/p2p-poker/internal/game"
	"github.com/p2p-poker/internal/tui"
)

// This entry point wires the Phase 4 TUI to the Phase 1 game engine
// running locally — no network required. It simulates a 4-player game
// where the human controls seat 0 ("You") and the other 3 seats are
// played by a simple bot that always calls or checks.
//
// In the final Phase 7 binary this is replaced by cmd/poker/main.go
// which wires the TUI to the full P2P network stack.

const (
	humanPlayerID = "human"
	numSeats      = 4
	startingStack = 1000
	smallBlind    = 5
	bigBlind      = 10
	botDelay      = 600 * time.Millisecond
)

// botActionCmd produces a tea.Cmd that fires after a short delay,
// simulating a bot player making their move.
func botActionCmd(gs *game.GameState, m *game.Machine) tea.Cmd {
	return tea.Tick(botDelay, func(t time.Time) tea.Msg {
		current := gs.CurrentPlayer()
		if current == nil || current.ID == humanPlayerID {
			return nil
		}
		// Simple bot: always call/check.
		toCall := gs.CurrentBet - current.CurrentBet
		var a game.Action
		if toCall > 0 {
			a = game.Action{PlayerID: current.ID, Type: game.ActionCall}
		} else {
			a = game.Action{PlayerID: current.ID, Type: game.ActionCheck}
		}
		_ = m.ApplyAction(a)
		return tui.GameStateMsg{State: gs}
	})
}

// newHandCmd sets up a new hand after settling.
func newHandCmd(players []*game.Player, dealerIdx *int, handNum *int, rng *rand.Rand) tea.Cmd {
	return tea.Tick(1500*time.Millisecond, func(t time.Time) tea.Msg {
		*handNum++
		*dealerIdx = (*dealerIdx + 1) % numSeats
		for _, p := range players {
			p.ResetForNewHand()
		}
		gs := game.NewGameState("local", *handNum, players, *dealerIdx, smallBlind, bigBlind)
		m := game.NewMachine(gs, rng)
		if err := m.StartHand(); err != nil {
			return tui.ErrorMsg{Text: err.Error()}
		}
		return tui.GameStateMsg{State: gs}
	})
}

// ── gameModel wraps the tui.Model with local game engine state ────────────────

type gameModel struct {
	ui        tui.Model
	gs        *game.GameState
	machine   *game.Machine
	players   []*game.Player
	dealerIdx int
	handNum   int
	rng       *rand.Rand
}

func newGameModel() gameModel {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Build players: seat 0 is the human, seats 1-3 are bots.
	players := []*game.Player{
		game.NewPlayer(humanPlayerID, "You", startingStack),
		game.NewPlayer("bot1", "Alice (bot)", startingStack),
		game.NewPlayer("bot2", "Bob (bot)", startingStack),
		game.NewPlayer("bot3", "Carol (bot)", startingStack),
	}

	dealerIdx := 0
	handNum := 1

	gs := game.NewGameState("local", handNum, players, dealerIdx, smallBlind, bigBlind)
	m := game.NewMachine(gs, rng)

	// Populate model with a callback that applies human actions.
	var gm gameModel
	ui := tui.NewModel(humanPlayerID, func(a game.Action) {
		if gm.gs == nil {
			return
		}
		if err := gm.machine.ApplyAction(a); err != nil {
			// The model.Update path handles errors via ErrorMsg — but since
			// we're in a callback we just log it here.
			fmt.Fprintf(os.Stderr, "action error: %v\n", err)
		}
	})

	gm = gameModel{
		ui:        ui,
		gs:        gs,
		machine:   m,
		players:   players,
		dealerIdx: dealerIdx,
		handNum:   handNum,
		rng:       rng,
	}

	return gm
}

func (gm gameModel) Init() tea.Cmd {
	// Start the first hand.
	return tea.Batch(
		tea.EnterAltScreen,
		tea.Cmd(func() tea.Msg {
			if err := gm.machine.StartHand(); err != nil {
				return tui.ErrorMsg{Text: err.Error()}
			}
			return tui.GameStateMsg{State: gm.gs}
		}),
	)
}

func (gm gameModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tui.GameStateMsg:
		// Update game state reference.
		gm.gs = msg.State

		// Feed into the TUI model.
		newUI, _ := gm.ui.Update(msg)
		gm.ui = newUI.(tui.Model)

		// If the hand settled, build winner message then queue next hand.
		if gm.gs.Phase == game.PhaseSettled {
			winnerIDs := make(map[string]bool)
			handRanks := make(map[string]string)
			for id, payout := range gm.gs.Payouts {
				if payout > 0 {
					winnerIDs[id] = true
				}
			}
			// Evaluate winning hands for label display.
			if len(gm.gs.CommunityCards) == 5 {
				for _, p := range gm.gs.Players {
					if p.Status != game.StatusFolded && winnerIDs[p.ID] {
						var seven [7]game.Card
						seven[0] = p.HoleCards[0]
						seven[1] = p.HoleCards[1]
						for i, c := range gm.gs.CommunityCards {
							seven[i+2] = c
						}
						h := game.EvaluateBest7(seven)
						handRanks[p.ID] = h.Rank.String()
					}
				}
			}
			winMsg := tui.WinnerMsg{
				WinnerIDs: winnerIDs,
				HandRanks: handRanks,
				Payouts:   gm.gs.Payouts,
			}
			newUI2, _ := gm.ui.Update(winMsg)
			gm.ui = newUI2.(tui.Model)

			return gm, newHandCmd(gm.players, &gm.dealerIdx, &gm.handNum, gm.rng)
		}

		// If it's a bot's turn, schedule their action.
		current := gm.gs.CurrentPlayer()
		if current != nil && current.ID != humanPlayerID && gm.gs.Phase != game.PhaseSettled {
			return gm, botActionCmd(gm.gs, gm.machine)
		}

		return gm, nil

	case tui.ErrorMsg:
		newUI, cmd := gm.ui.Update(msg)
		gm.ui = newUI.(tui.Model)
		return gm, cmd

	case tui.WinnerMsg:
		newUI, cmd := gm.ui.Update(msg)
		gm.ui = newUI.(tui.Model)
		return gm, cmd

	case tea.KeyMsg:
		// If it's the human's turn, the TUI handles the key and fires OnAction.
		newUI, cmd := gm.ui.Update(msg)
		gm.ui = newUI.(tui.Model)

		// After human action, the game state has been mutated in place.
		// Re-send current state to keep the TUI in sync.
		if gm.gs != nil && gm.ui.Mode == tui.ModeSpectate {
			gsMsg := tea.Cmd(func() tea.Msg {
				return tui.GameStateMsg{State: gm.gs}
			})
			return gm, tea.Batch(cmd, gsMsg)
		}
		return gm, cmd

	default:
		newUI, cmd := gm.ui.Update(msg)
		gm.ui = newUI.(tui.Model)
		return gm, cmd
	}
}

func (gm gameModel) View() string {
	return gm.ui.View()
}

func main() {
	gm := newGameModel()
	p := tea.NewProgram(gm, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}
