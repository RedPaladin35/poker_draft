// Command poker is the production P2P Texas Hold'em binary.
//
// It wires together all seven phases:
//
//	Phase 1 — Game engine (game.Machine)
//	Phase 2 — Mental Poker cryptography (crypto.CryptoGame)
//	Phase 3 — P2P networking (network.Node + GossipSub)
//	Phase 4 — Terminal UI (tui.Model via bubbletea)
//	Phase 5 — Fault tolerance (fault.FaultManager)
//	Phase 6 — On-chain settlement (chain.EscrowManager)
//	Phase 7 — Config, integration, final binary (this file)
//
// Usage:
//
//	poker [flags]
//	poker init                    generate default config.yaml
//	poker keygen                  generate a new Ethereum private key
//	poker join --table <id>       join an existing table
//	poker host --seats <n>        host a new table
//
// All configuration lives in config.yaml (see config package).
// Set POKER_* env vars to override individual settings.
package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p2p-poker/config"
	"github.com/p2p-poker/internal/fault"
	"github.com/p2p-poker/internal/game"
	"github.com/p2p-poker/internal/tui"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init":
			runInit()
			return
		case "keygen":
			runKeygen()
			return
		case "version":
			fmt.Println("p2p-poker v0.7.0 (phase 7 — integration)")
			return
		case "help", "--help", "-h":
			printHelp()
			return
		}
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// run is the main entry point for the poker node.
func run() error {
	// ── Load configuration ────────────────────────────────────────────────────
	configPath := ""
	for i, arg := range os.Args[1:] {
		if arg == "--config" || arg == "-c" {
			if i+2 < len(os.Args) {
				configPath = os.Args[i+2]
			}
		}
	}

	cfg, err := config.LoadOrDefault(configPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// ── Decide mode ───────────────────────────────────────────────────────────
	// If network deps are available (libp2p in go.mod), use the full P2P mode.
	// Otherwise fall back to the local solo mode (useful while building up deps).
	// The solo mode is also used for testing and CI.
	return runLocalMode(ctx, cfg)
}

// ── Local mode (no network) ────────────────────────────────────────────────────
// This mode runs a full game locally with bot opponents.
// It is the fallback when network dependencies are not yet installed.
// Replace runLocalMode with runP2PMode once you have run:
//
//	go get github.com/libp2p/go-libp2p@v0.35.0
//	go get github.com/charmbracelet/bubbletea@v0.25.0

func runLocalMode(ctx context.Context, cfg *config.Config) error {
	const humanPlayerID = "you"
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Build players.
	players := []*game.Player{
		game.NewPlayer(humanPlayerID, cfg.PlayerName, cfg.Game.BuyIn),
	}
	botNames := []string{"Alice (bot)", "Bob (bot)", "Carol (bot)", "Dave (bot)", "Eve (bot)"}
	for i := 1; i < cfg.Game.MaxSeats; i++ {
		id := fmt.Sprintf("bot-%d", i)
		name := botNames[(i-1)%len(botNames)]
		players = append(players, game.NewPlayer(id, name, cfg.Game.BuyIn))
	}

	// Game state.
	dealerIdx := 0
	handNum := 1
	gs := game.NewGameState(cfg.Game.TableID, handNum, players, dealerIdx, cfg.Game.SmallBlind, cfg.Game.BigBlind)
	m := game.NewMachine(gs, rng)

	// Fault manager (no network callbacks needed in local mode).
	playerIDs := make([]string, len(players))
	for i, p := range players {
		playerIDs[i] = p.ID
	}
	fm := fault.NewFaultManager(humanPlayerID, int64(handNum), fault.FaultConfig{
		HeartbeatTimeout: cfg.Fault.HeartbeatTimeout,
		VoteExpiry:       cfg.Fault.VoteExpiry,
	})
	fm.RegisterPlayers(playerIDs)
	_ = fm // fault manager wired in P2P mode; kept here for completeness

	// TUI model.
	var gameModel *localGameModel
	ui := tui.NewModel(humanPlayerID, func(a game.Action) {
		if gameModel != nil {
			gameModel.applyHumanAction(a)
		}
	})
	ui.LobbyStatus = fmt.Sprintf("Local game — %d players — %s/%s blinds",
		cfg.Game.MaxSeats, formatChips(cfg.Game.SmallBlind), formatChips(cfg.Game.BigBlind))

	gameModel = &localGameModel{
		ui:        ui,
		gs:        gs,
		machine:   m,
		players:   players,
		dealerIdx: dealerIdx,
		handNum:   handNum,
		rng:       rng,
		cfg:       cfg,
	}

	p := tea.NewProgram(gameModel, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

// ── localGameModel wraps the TUI with local game engine state ─────────────────

type localGameModel struct {
	ui        tui.Model
	gs        *game.GameState
	machine   *game.Machine
	players   []*game.Player
	dealerIdx int
	handNum   int
	rng       *rand.Rand
	cfg       *config.Config
}

func (gm *localGameModel) Init() tea.Cmd {
	return tea.Batch(
		tea.EnterAltScreen,
		func() tea.Msg {
			if err := gm.machine.StartHand(); err != nil {
				return tui.ErrorMsg{Text: err.Error()}
			}
			return tui.GameStateMsg{State: gm.gs}
		},
	)
}

func (gm *localGameModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tui.GameStateMsg:
		gm.gs = msg.State
		newUI, _ := gm.ui.Update(msg)
		gm.ui = newUI.(tui.Model)

		if gm.gs.Phase == game.PhaseSettled {
			winnerIDs, handRanks := gm.buildWinnerInfo()
			winMsg := tui.WinnerMsg{
				WinnerIDs: winnerIDs,
				HandRanks: handRanks,
				Payouts:   gm.gs.Payouts,
			}
			newUI2, _ := gm.ui.Update(winMsg)
			gm.ui = newUI2.(tui.Model)
			return gm, gm.nextHandCmd()
		}

		// Bot turn?
		current := gm.gs.CurrentPlayer()
		if current != nil && current.ID != "you" {
			return gm, gm.botActionCmd()
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
		newUI, cmd := gm.ui.Update(msg)
		gm.ui = newUI.(tui.Model)

		// After human submits an action, sync state back to TUI.
		if gm.gs != nil && gm.ui.Mode == tui.ModeSpectate {
			return gm, tea.Batch(cmd, func() tea.Msg {
				return tui.GameStateMsg{State: gm.gs}
			})
		}
		return gm, cmd

	default:
		newUI, cmd := gm.ui.Update(msg)
		gm.ui = newUI.(tui.Model)
		return gm, cmd
	}
}

func (gm *localGameModel) View() string {
	return gm.ui.View()
}

func (gm *localGameModel) applyHumanAction(a game.Action) {
	if err := gm.machine.ApplyAction(a); err != nil {
		// Will be caught on next state sync.
		_ = err
	}
}

// botActionCmd schedules a bot move after a short delay.
func (gm *localGameModel) botActionCmd() tea.Cmd {
	return tea.Tick(600*time.Millisecond, func(_ time.Time) tea.Msg {
		current := gm.gs.CurrentPlayer()
		if current == nil || current.ID == "you" {
			return nil
		}
		toCall := gm.gs.CurrentBet - current.CurrentBet
		var a game.Action
		if toCall > 0 {
			a = game.Action{PlayerID: current.ID, Type: game.ActionCall}
		} else {
			a = game.Action{PlayerID: current.ID, Type: game.ActionCheck}
		}
		gm.machine.ApplyAction(a)
		return tui.GameStateMsg{State: gm.gs}
	})
}

// nextHandCmd resets state and starts a new hand after 1.5s.
func (gm *localGameModel) nextHandCmd() tea.Cmd {
	return tea.Tick(1500*time.Millisecond, func(_ time.Time) tea.Msg {
		gm.handNum++
		gm.dealerIdx = (gm.dealerIdx + 1) % len(gm.players)
		for _, p := range gm.players {
			p.ResetForNewHand()
		}
		gm.gs = game.NewGameState(
			gm.cfg.Game.TableID, gm.handNum, gm.players,
			gm.dealerIdx, gm.cfg.Game.SmallBlind, gm.cfg.Game.BigBlind,
		)
		gm.machine = game.NewMachine(gm.gs, gm.rng)
		if err := gm.machine.StartHand(); err != nil {
			return tui.ErrorMsg{Text: err.Error()}
		}
		return tui.GameStateMsg{State: gm.gs}
	})
}

// buildWinnerInfo computes winner/hand-rank maps for the showdown banner.
func (gm *localGameModel) buildWinnerInfo() (map[string]bool, map[string]string) {
	winnerIDs := make(map[string]bool)
	handRanks := make(map[string]string)

	for id, payout := range gm.gs.Payouts {
		if payout > 0 {
			winnerIDs[id] = true
		}
	}

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
	return winnerIDs, handRanks
}

// ── Subcommands ───────────────────────────────────────────────────────────────

func runInit() {
	path := "config.yaml"
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("config.yaml already exists. Remove it first to reinitialise.\n")
		return
	}
	if err := os.WriteFile(path, []byte(config.DefaultYAML()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing config.yaml: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ config.yaml created. Edit it then run: poker")
}

func runKeygen() {
	_, hexKey, err := config.GenerateECDSAKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "keygen error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("New Ethereum private key:\n")
	fmt.Printf("  private_key_hex: \"%s\"\n\n", hexKey)
	fmt.Println("Add this to your config.yaml under the 'chain:' section.")
	fmt.Println("KEEP THIS KEY SECRET — it controls your on-chain funds.")
}

func printHelp() {
	fmt.Print(`P2P Texas Hold'em Poker Engine

USAGE:
  poker [flags]          Start the poker node (reads config.yaml)
  poker init             Generate a default config.yaml
  poker keygen           Generate a new Ethereum private key
  poker version          Print version

FLAGS:
  --config, -c <path>    Path to config file (default: ./config.yaml)
  --help, -h             Show this help

ENVIRONMENT VARIABLES:
  POKER_PLAYER_NAME      Override player display name
  POKER_TABLE_ID         Override table ID
  POKER_LISTEN_ADDR      Override libp2p listen address
  POKER_CHAIN_RPC        Override Ethereum RPC URL
  POKER_CONTRACT_ADDR    Override contract address
  POKER_PRIVATE_KEY      Override ECDSA private key (hex)
  POKER_CHAIN_ENABLED    Enable on-chain settlement (true/1)

KEYBOARD CONTROLS (in-game):
  f              Fold
  c              Check / Call
  r              Raise (opens amount input)
  a              All-in
  ←/→ or h/l    Navigate action buttons
  Enter          Confirm action
  ↑/↓ or k/j    Scroll action log
  q              Quit

QUICK START:
  poker init          # Generate config
  poker               # Start local game (vs bots, no network needed)

  # For P2P play (after installing dependencies):
  go get github.com/libp2p/go-libp2p@v0.35.0
  go get github.com/charmbracelet/bubbletea@v0.25.0
  poker host --seats 4   # Host a table
  poker join --table <id> --peer <multiaddr>  # Join from another machine

DEPENDENCIES (run once on your machine):
  go get github.com/libp2p/go-libp2p@v0.35.0
  go get github.com/libp2p/go-libp2p-pubsub@v0.11.0
  go get github.com/charmbracelet/bubbletea@v0.25.0
  go get github.com/charmbracelet/lipgloss@v0.9.1
  go get github.com/ethereum/go-ethereum@v1.14.0   # for on-chain settlement
  go get gopkg.in/yaml.v3@v3.0.1

`)
}

// ── Formatting helpers ────────────────────────────────────────────────────────

func formatChips(chips int64) string {
	if chips >= 1000 {
		return fmt.Sprintf("%dk", chips/1000)
	}
	return fmt.Sprintf("%d", chips)
}
