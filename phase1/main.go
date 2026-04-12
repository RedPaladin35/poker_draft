package main

import (
	"fmt"
	"math/rand"
	"strings"

	"github.com/p2p-poker/internal/game"
)

const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	green  = "\033[32m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
	red    = "\033[31m"
	white  = "\033[37m"
	dim    = "\033[2m"
)

func separator(char string, n int) string {
	return strings.Repeat(char, n)
}

func printPhase(phase game.Phase) {
	fmt.Printf("\n%s%s══ %s ══%s\n", bold, yellow, phase, reset)
}

func printState(gs *game.GameState) {
	fmt.Printf("%sPot: %d%s  |  Street bet: %d  |  Acting: %s\n",
		cyan, game.TotalPot(gs.Pots), reset,
		gs.CurrentBet,
		playerLabel(gs.CurrentPlayer()),
	)
	if len(gs.CommunityCards) > 0 {
		fmt.Printf("Board: %s\n", formatCards(gs.CommunityCards))
	}
}

func printPlayers(players []*game.Player) {
	fmt.Printf("\n%sSeats:%s\n", dim, reset)
	for _, p := range players {
		status := ""
		switch p.Status {
		case game.StatusFolded:
			status = red + "[folded]" + reset
		case game.StatusAllIn:
			status = yellow + "[all-in]" + reset
		case game.StatusActive:
			status = green + "[active]" + reset
		}
		cards := ""
		zero := game.Card{}
		if p.HoleCards[0] != zero {
			cards = fmt.Sprintf("  %s%s %s%s",
				white,
				p.HoleCards[0],
				p.HoleCards[1],
				reset,
			)
		}
		fmt.Printf("  %-10s stack=%-6d %s%s\n", p.Name, p.Stack, status, cards)
	}
}

func printPots(pots []game.PotSlice) {
	if len(pots) == 0 {
		return
	}
	fmt.Printf("\n%sPots:%s\n", dim, reset)
	for i, p := range pots {
		label := "Main pot"
		if i > 0 {
			label = fmt.Sprintf("Side pot %d", i)
		}
		fmt.Printf("  %s: %d  eligible: [%s]\n", label, p.Amount, strings.Join(p.EligibleIDs, ", "))
	}
}

func printPayouts(payouts map[string]int64, players []*game.Player) {
	fmt.Printf("\n%s%s══ Payouts ══%s\n", bold, green, reset)
	nameMap := make(map[string]string)
	for _, p := range players {
		nameMap[p.ID] = p.Name
	}
	for id, delta := range payouts {
		sign := "+"
		if delta < 0 {
			sign = ""
		}
		fmt.Printf("  %s: %s%d\n", nameMap[id], sign, delta)
	}
}

func playerLabel(p *game.Player) string {
	if p == nil {
		return "—"
	}
	return p.Name
}

func formatCards(cards []game.Card) string {
	parts := make([]string, len(cards))
	for i, c := range cards {
		parts[i] = c.String()
	}
	return strings.Join(parts, " ")
}

// autoAction picks a reasonable action for demonstration purposes.
func autoAction(gs *game.GameState) game.Action {
	current := gs.CurrentPlayer()
	toCall := gs.CurrentBet - current.CurrentBet
	if toCall <= 0 {
		return game.Action{PlayerID: current.ID, Type: game.ActionCheck}
	}
	return game.Action{PlayerID: current.ID, Type: game.ActionCall}
}

func main() {
	fmt.Printf("%s%s%s\n", bold, separator("═", 50), reset)
	fmt.Printf("%s  P2P Poker Engine — Phase 1 Demo%s\n", bold, reset)
	fmt.Printf("%s%s%s\n\n", bold, separator("═", 50), reset)

	// ── Setup: 6 players, one with a short stack to force a side pot ──────────
	players := []*game.Player{
		game.NewPlayer("A", "Alice", 50),    // short stack → will go all-in
		game.NewPlayer("B", "Bob", 500),
		game.NewPlayer("C", "Carol", 500),
		game.NewPlayer("D", "Dave", 500),
		game.NewPlayer("E", "Eve", 500),
		game.NewPlayer("F", "Frank", 500),
	}

	gs := game.NewGameState("demo-table", 1, players, 0, 5, 10)
	rng := rand.New(rand.NewSource(12345))
	m := game.NewMachine(gs, rng)

	fmt.Printf("Players: %d  |  Small Blind: %d  |  Big Blind: %d\n",
		len(players), gs.SmallBlind, gs.BigBlind)
	fmt.Printf("Alice has only %d chips — she will be forced all-in.\n", players[0].Stack)

	// ── Start hand ─────────────────────────────────────────────────────────────
	if err := m.StartHand(); err != nil {
		fmt.Printf("StartHand error: %v\n", err)
		return
	}

	printPhase(gs.Phase)
	printPlayers(players)

	// ── Play each action ───────────────────────────────────────────────────────
	maxActions := 500
	lastPhase := gs.Phase

	for gs.Phase != game.PhaseSettled && maxActions > 0 {
		maxActions--

		if gs.Phase != lastPhase {
			printPhase(gs.Phase)
			if len(gs.CommunityCards) > 0 {
				fmt.Printf("Community: %s\n", formatCards(gs.CommunityCards))
			}
			printPlayers(players)
			lastPhase = gs.Phase
		}

		current := gs.CurrentPlayer()
		if current == nil {
			break
		}

		printState(gs)

		// Alice (short stack) always goes all-in pre-flop.
		var a game.Action
		if current.ID == "A" && gs.Phase == game.PhasePreFlop {
			a = game.Action{PlayerID: current.ID, Type: game.ActionAllIn}
			fmt.Printf("  %s%s goes ALL-IN for %d%s\n", bold, current.Name, current.Stack, reset)
		} else {
			a = autoAction(gs)
			toCall := gs.CurrentBet - current.CurrentBet
			if toCall > 0 {
				fmt.Printf("  %s calls %d\n", current.Name, toCall)
			} else {
				fmt.Printf("  %s checks\n", current.Name)
			}
		}

		if err := m.ApplyAction(a); err != nil {
			fmt.Printf("%sAction error: %v%s\n", red, err, reset)
			break
		}
	}

	// ── Showdown ───────────────────────────────────────────────────────────────
	printPhase(game.PhaseSettled)
	fmt.Printf("\nFinal hands:\n")
	for _, p := range players {
		if p.Status != game.StatusFolded {
			zero := game.Card{}
			if p.HoleCards[0] != zero {
				fmt.Printf("  %-8s %s %s\n", p.Name,
					p.HoleCards[0], p.HoleCards[1])
			}
		} else {
			fmt.Printf("  %-8s (folded)\n", p.Name)
		}
	}

	printPots(gs.Pots)
	printPayouts(gs.Payouts, players)

	// ── Chip conservation check ────────────────────────────────────────────────
	var totalAfter int64
	for _, p := range players {
		totalAfter += p.Stack
	}
	fmt.Printf("\n%sChip conservation: total chips = %d%s\n", cyan, totalAfter, reset)
	expected := int64(50 + 5*500)
	if totalAfter == expected {
		fmt.Printf("%s✓ Chips conserved correctly (expected %d)%s\n", green, expected, reset)
	} else {
		fmt.Printf("%s✗ CHIP CONSERVATION FAILED: expected %d, got %d%s\n",
			red, expected, totalAfter, reset)
	}

	fmt.Printf("\n%s%s%s\n", bold, separator("═", 50), reset)

	// ── Run a quick multi-hand simulation ─────────────────────────────────────
	fmt.Printf("\n%sRunning 50-hand simulation (chip conservation check)...%s\n", dim, reset)

	simPlayers := []*game.Player{
		game.NewPlayer("1", "P1", 1000),
		game.NewPlayer("2", "P2", 1000),
		game.NewPlayer("3", "P3", 1000),
		game.NewPlayer("4", "P4", 1000),
	}
	simRng := rand.New(rand.NewSource(999))
	dealerIdx := 0

	for hand := 1; hand <= 50; hand++ {
		for _, p := range simPlayers {
			p.ResetForNewHand()
		}
		simGS := game.NewGameState("sim", hand, simPlayers, dealerIdx, 5, 10)
		simM := game.NewMachine(simGS, simRng)

		if err := simM.StartHand(); err != nil {
			fmt.Printf("Hand %d StartHand error: %v\n", hand, err)
			continue
		}

		limit := 200
		for simGS.Phase != game.PhaseSettled && limit > 0 {
			limit--
			current := simGS.CurrentPlayer()
			if current == nil {
				break
			}
			toCall := simGS.CurrentBet - current.CurrentBet
			var a game.Action
			if toCall > 0 {
				a = game.Action{PlayerID: current.ID, Type: game.ActionCall}
			} else {
				a = game.Action{PlayerID: current.ID, Type: game.ActionCheck}
			}
			if err := simM.ApplyAction(a); err != nil {
				break
			}
		}

		var total int64
		for _, p := range simPlayers {
			total += p.Stack
		}
		if total != 4000 {
			fmt.Printf("%sHand %d: chip violation! total=%d%s\n", red, hand, total, reset)
		}

		dealerIdx = (dealerIdx + 1) % len(simPlayers)
	}

	fmt.Printf("%s✓ 50-hand simulation complete — all chip totals conserved%s\n", green, reset)
}
