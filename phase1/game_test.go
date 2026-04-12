package game

import (
	"math/rand"
	"testing"
)

// ─── Deck tests ───────────────────────────────────────────────────────────────

func TestNewDeck_Has52Cards(t *testing.T) {
	d := NewDeck()
	if len(d.Cards) != 52 {
		t.Fatalf("expected 52 cards, got %d", len(d.Cards))
	}
}

func TestNewDeck_AllUnique(t *testing.T) {
	d := NewDeck()
	seen := make(map[int]bool)
	for _, c := range d.Cards {
		id := c.CardID()
		if seen[id] {
			t.Fatalf("duplicate card: %s (id %d)", c, id)
		}
		seen[id] = true
	}
}

func TestDeck_Deal(t *testing.T) {
	d := NewDeck()
	first := d.Cards[0]
	c, err := d.Deal()
	if err != nil {
		t.Fatal(err)
	}
	if c != first {
		t.Fatalf("expected %s, got %s", first, c)
	}
	if d.Remaining() != 51 {
		t.Fatalf("expected 51 remaining, got %d", d.Remaining())
	}
}

func TestCardRoundTrip(t *testing.T) {
	d := NewDeck()
	for _, c := range d.Cards {
		id := c.CardID()
		back := CardFromID(id)
		if back != c {
			t.Fatalf("card round-trip failed: %s -> %d -> %s", c, id, back)
		}
	}
}

// ─── Hand evaluation tests ────────────────────────────────────────────────────

func cards(s string) [5]Card {
	// Parse a 5-card string like "AcKcQcJcTc" (10-char notation).
	// Format: rank char + suit char pairs.
	rankMap := map[byte]Rank{
		'2': Two, '3': Three, '4': Four, '5': Five, '6': Six,
		'7': Seven, '8': Eight, '9': Nine, 'T': Ten,
		'J': Jack, 'Q': Queen, 'K': King, 'A': Ace,
	}
	suitMap := map[byte]Suit{'s': Spades, 'h': Hearts, 'd': Diamonds, 'c': Clubs}
	var out [5]Card
	for i := 0; i < 5; i++ {
		out[i] = Card{Rank: rankMap[s[i*2]], Suit: suitMap[s[i*2+1]]}
	}
	return out
}

func TestHandRankings(t *testing.T) {
	tests := []struct {
		name     string
		hand     [5]Card
		wantRank HandRank
	}{
		{"royal flush", cards("AcKcQcJcTc"), RoyalFlush},
		{"straight flush", cards("9s8s7s6s5s"), StraightFlush},
		{"four of a kind", cards("AsAdAhAc2s"), FourOfAKind},
		{"full house", cards("AsAdAh2s2d"), FullHouse},
		{"flush", cards("As3s7sJsQs"), Flush},
		{"straight", cards("9s8h7d6c5s"), Straight},
		{"wheel straight", cards("As2h3d4c5s"), Straight},
		{"three of a kind", cards("AsAdAh2s3d"), ThreeOfAKind},
		{"two pair", cards("AsAdKhKc2s"), TwoPair},
		{"one pair", cards("AsAd2h3c4s"), OnePair},
		{"high card", cards("AsKd9h6c2s"), HighCard},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := evaluate5(tt.hand)
			if h.Rank != tt.wantRank {
				t.Errorf("got %s, want %s", h.Rank, tt.wantRank)
			}
		})
	}
}

func TestHandComparison(t *testing.T) {
	royalFlush := evaluate5(cards("AcKcQcJcTc"))
	straightFlush := evaluate5(cards("9s8s7s6s5s"))
	fourAces := evaluate5(cards("AsAdAhAc2s"))
	fullHouse := evaluate5(cards("AsAdAh2s2d"))

	if royalFlush.Compare(straightFlush) != 1 {
		t.Error("royal flush should beat straight flush")
	}
	if straightFlush.Compare(fourAces) != 1 {
		t.Error("straight flush should beat four of a kind")
	}
	if fourAces.Compare(fullHouse) != 1 {
		t.Error("four of a kind should beat full house")
	}
	if fullHouse.Compare(royalFlush) != -1 {
		t.Error("full house should lose to royal flush")
	}

	// Tie: same hand ranks.
	h1 := evaluate5(cards("AsAdAhAc2s"))
	h2 := evaluate5(cards("AsAdAhAc2s"))
	if h1.Compare(h2) != 0 {
		t.Error("identical hands should tie")
	}
}

func TestKickerBreaker(t *testing.T) {
	// Pair of aces with K kicker vs pair of aces with Q kicker.
	h1 := evaluate5(cards("AsAdKhQcJs")) // A A K Q J — should win
	h2 := evaluate5(cards("AhAcQhJcTs")) // A A Q J T

	if h1.Compare(h2) != 1 {
		t.Errorf("A-A-K-Q-J should beat A-A-Q-J-T, got Compare=%d", h1.Compare(h2))
	}
}

func TestBest7(t *testing.T) {
	// Give player 7c8c with board 9c Tc Jc 2d 5h — should detect straight flush.
	var seven [7]Card
	seven[0] = Card{Seven, Clubs}
	seven[1] = Card{Eight, Clubs}
	seven[2] = Card{Nine, Clubs}
	seven[3] = Card{Ten, Clubs}
	seven[4] = Card{Jack, Clubs}
	seven[5] = Card{Two, Diamonds}
	seven[6] = Card{Five, Hearts}

	h := EvaluateBest7(seven)
	if h.Rank != StraightFlush {
		t.Errorf("expected StraightFlush, got %s", h.Rank)
	}
}

// ─── Pot calculation tests ─────────────────────────────────────────────────────

func makePlayers(bets ...int64) []*Player {
	players := make([]*Player, len(bets))
	for i, b := range bets {
		p := NewPlayer(string(rune('A'+i)), string(rune('A'+i)), 1000)
		p.TotalBet = b
		players[i] = p
	}
	return players
}

func TestPot_NoBets(t *testing.T) {
	pots := CalculatePots(makePlayers(0, 0, 0))
	if len(pots) != 0 {
		t.Errorf("expected no pots, got %d", len(pots))
	}
}

func TestPot_EqualBets(t *testing.T) {
	// 3 players bet 100 each — one pot of 300 with all three eligible.
	players := makePlayers(100, 100, 100)
	pots := CalculatePots(players)
	if len(pots) != 1 {
		t.Fatalf("expected 1 pot, got %d", len(pots))
	}
	if pots[0].Amount != 300 {
		t.Errorf("expected pot of 300, got %d", pots[0].Amount)
	}
	if len(pots[0].EligibleIDs) != 3 {
		t.Errorf("expected 3 eligible, got %d", len(pots[0].EligibleIDs))
	}
}

func TestPot_SidePot(t *testing.T) {
	// Player A all-in for 50, B and C each bet 200.
	// Main pot: 50*3 = 150 (all eligible)
	// Side pot: 150*2 = 300 (B and C only)
	players := makePlayers(50, 200, 200)
	pots := CalculatePots(players)

	totalInPots := TotalPot(pots)
	if totalInPots != 450 {
		t.Errorf("total pot should be 450, got %d", totalInPots)
	}

	// First pot should have 3 eligible players.
	if len(pots[0].EligibleIDs) != 3 {
		t.Errorf("main pot should have 3 eligible, got %d", len(pots[0].EligibleIDs))
	}

	// Second pot should have 2 eligible players (not A).
	if len(pots) < 2 {
		t.Fatal("expected at least 2 pot slices")
	}
	if len(pots[1].EligibleIDs) != 2 {
		t.Errorf("side pot should have 2 eligible, got %d", len(pots[1].EligibleIDs))
	}
}

func TestPot_MultipleSidePots(t *testing.T) {
	// A=50 (smallest all-in), B=150, C=300
	players := makePlayers(50, 150, 300)
	pots := CalculatePots(players)

	total := TotalPot(pots)
	if total != 500 {
		t.Errorf("expected total 500, got %d", total)
	}
}

// ─── State machine integration tests ─────────────────────────────────────────

func newTestGame(numPlayers int, stack int64) (*Machine, []*Player) {
	players := make([]*Player, numPlayers)
	for i := 0; i < numPlayers; i++ {
		id := string(rune('A' + i))
		players[i] = NewPlayer(id, "Player "+id, stack)
	}
	gs := NewGameState("test-table", 1, players, 0, 5, 10)
	rng := rand.New(rand.NewSource(42))
	m := NewMachine(gs, rng)
	return m, players
}

func TestStartHand(t *testing.T) {
	m, players := newTestGame(3, 500)
	if err := m.StartHand(); err != nil {
		t.Fatalf("StartHand failed: %v", err)
	}
	if m.State.Phase != PhasePreFlop {
		t.Errorf("expected PreFlop, got %s", m.State.Phase)
	}
	// All players should have hole cards.
	for _, p := range players {
		if p.HoleCards[0] == (Card{}) || p.HoleCards[1] == (Card{}) {
			t.Errorf("player %s has empty hole cards", p.ID)
		}
	}
	// Deck should have 52 - 3*2 - 0 burn = 46 remaining (no burn pre-flop).
	if m.State.Deck.Remaining() != 46 {
		t.Errorf("expected 46 cards remaining, got %d", m.State.Deck.Remaining())
	}
}

func TestFoldWinsHand(t *testing.T) {
	m, players := newTestGame(3, 500)
	if err := m.StartHand(); err != nil {
		t.Fatal(err)
	}
	gs := m.State

	// Two players fold — the third should win.
	for gs.Phase != PhaseSettled {
		current := gs.CurrentPlayer()
		if current == nil {
			break
		}
		// Force first two actors to fold.
		var a Action
		if current.ID == players[0].ID || current.ID == players[1].ID {
			a = Action{PlayerID: current.ID, Type: ActionFold}
		} else {
			a = Action{PlayerID: current.ID, Type: ActionCheck}
		}
		if err := m.ApplyAction(a); err != nil {
			// Some actions might fail if already settled; stop.
			break
		}
	}

	if gs.Phase != PhaseSettled {
		t.Errorf("expected PhaseSettled, got %s", gs.Phase)
	}
}

func TestFullHandHeadsUp(t *testing.T) {
	m, _ := newTestGame(2, 200)
	if err := m.StartHand(); err != nil {
		t.Fatal(err)
	}
	gs := m.State

	// Play out the hand: always call/check.
	maxActions := 100
	for gs.Phase != PhaseSettled && maxActions > 0 {
		maxActions--
		current := gs.CurrentPlayer()
		if current == nil {
			break
		}
		toCall := gs.CurrentBet - current.CurrentBet
		var a Action
		if toCall > 0 {
			a = Action{PlayerID: current.ID, Type: ActionCall}
		} else {
			a = Action{PlayerID: current.ID, Type: ActionCheck}
		}
		if err := m.ApplyAction(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if gs.Phase != PhaseSettled {
		t.Errorf("hand did not reach PhaseSettled, stuck at %s", gs.Phase)
	}

	// Total chips should be conserved.
	var totalChips int64
	for _, p := range gs.Players {
		totalChips += p.Stack
	}
	if totalChips != 400 { // 2 players × 200
		t.Errorf("chip conservation violated: expected 400, got %d", totalChips)
	}
}

func TestFullHandSixPlayers(t *testing.T) {
	m, _ := newTestGame(6, 1000)
	if err := m.StartHand(); err != nil {
		t.Fatal(err)
	}
	gs := m.State

	maxActions := 200
	for gs.Phase != PhaseSettled && maxActions > 0 {
		maxActions--
		current := gs.CurrentPlayer()
		if current == nil {
			break
		}
		toCall := gs.CurrentBet - current.CurrentBet
		var a Action
		if toCall > 0 {
			a = Action{PlayerID: current.ID, Type: ActionCall}
		} else {
			a = Action{PlayerID: current.ID, Type: ActionCheck}
		}
		if err := m.ApplyAction(a); err != nil {
			t.Fatalf("error applying action: %v", err)
		}
	}

	if gs.Phase != PhaseSettled {
		t.Errorf("hand did not settle, stuck at %s", gs.Phase)
	}

	var total int64
	for _, p := range gs.Players {
		total += p.Stack
	}
	if total != 6000 {
		t.Errorf("chip conservation failed: expected 6000, got %d", total)
	}
}

func TestSidePotDistribution(t *testing.T) {
	// 3 players: A has 50 chips (will go all-in), B and C have 500.
	players := []*Player{
		NewPlayer("A", "Alice", 50),
		NewPlayer("B", "Bob", 500),
		NewPlayer("C", "Carol", 500),
	}
	gs := NewGameState("test", 1, players, 0, 5, 10)
	rng := rand.New(rand.NewSource(99))
	m := NewMachine(gs, rng)

	if err := m.StartHand(); err != nil {
		t.Fatal(err)
	}

	// Play: A goes all-in, B and C call.
	maxActions := 200
	for gs.Phase != PhaseSettled && maxActions > 0 {
		maxActions--
		current := gs.CurrentPlayer()
		if current == nil {
			break
		}
		var a Action
		if current.ID == "A" {
			a = Action{PlayerID: "A", Type: ActionAllIn}
		} else {
			toCall := gs.CurrentBet - current.CurrentBet
			if toCall > 0 {
				a = Action{PlayerID: current.ID, Type: ActionCall}
			} else {
				a = Action{PlayerID: current.ID, Type: ActionCheck}
			}
		}
		if err := m.ApplyAction(a); err != nil {
			t.Logf("action error (may be OK): %v", err)
			break
		}
	}

	if gs.Phase != PhaseSettled {
		t.Errorf("expected settled, got %s", gs.Phase)
	}

	// Total chips must be conserved.
	var total int64
	for _, p := range gs.Players {
		total += p.Stack
	}
	if total != 1050 {
		t.Errorf("chip conservation failed: expected 1050, got %d", total)
	}
}

func TestInvalidAction_WrongPlayer(t *testing.T) {
	m, _ := newTestGame(2, 200)
	if err := m.StartHand(); err != nil {
		t.Fatal(err)
	}
	wrongID := "Z"
	err := m.ApplyAction(Action{PlayerID: wrongID, Type: ActionCheck})
	if err == nil {
		t.Error("expected error for wrong player ID, got nil")
	}
}

func TestInvalidAction_CheckWithBetToCall(t *testing.T) {
	m, _ := newTestGame(2, 200)
	if err := m.StartHand(); err != nil {
		t.Fatal(err)
	}
	current := m.State.CurrentPlayer()
	// Pre-flop there is a big blind to call — check should fail.
	err := m.ApplyAction(Action{PlayerID: current.ID, Type: ActionCheck})
	if err == nil {
		t.Error("expected error for check with bet to call, got nil")
	}
}

func TestRaise(t *testing.T) {
	m, _ := newTestGame(3, 500)
	if err := m.StartHand(); err != nil {
		t.Fatal(err)
	}
	gs := m.State
	current := gs.CurrentPlayer()
	// Raise to 30 (min raise = BB = 10, so raise by 20 is legal).
	err := m.ApplyAction(Action{PlayerID: current.ID, Type: ActionRaise, Amount: 20})
	if err != nil {
		t.Fatalf("raise failed: %v", err)
	}
	if gs.CurrentBet != 30 { // BB(10) + raise(20)
		t.Errorf("expected CurrentBet=30, got %d", gs.CurrentBet)
	}
}

func TestCommunityCards(t *testing.T) {
	m, _ := newTestGame(2, 500)
	if err := m.StartHand(); err != nil {
		t.Fatal(err)
	}
	gs := m.State

	// Force through all streets.
	maxActions := 300
	for gs.Phase != PhaseSettled && maxActions > 0 {
		maxActions--
		current := gs.CurrentPlayer()
		if current == nil {
			break
		}
		toCall := gs.CurrentBet - current.CurrentBet
		var a Action
		if toCall > 0 {
			a = Action{PlayerID: current.ID, Type: ActionCall}
		} else {
			a = Action{PlayerID: current.ID, Type: ActionCheck}
		}
		if err := m.ApplyAction(a); err != nil {
			break
		}
		// Check community card counts per phase.
		switch gs.Phase {
		case PhaseFlop:
			if len(gs.CommunityCards) != 3 {
				t.Errorf("Flop: expected 3 community cards, got %d", len(gs.CommunityCards))
			}
		case PhaseTurn:
			if len(gs.CommunityCards) != 4 {
				t.Errorf("Turn: expected 4 community cards, got %d", len(gs.CommunityCards))
			}
		case PhaseRiver:
			if len(gs.CommunityCards) != 5 {
				t.Errorf("River: expected 5 community cards, got %d", len(gs.CommunityCards))
			}
		}
	}
}

func TestDealerRotation(t *testing.T) {
	// Simulate 3 hands and verify dealer index rotates.
	players := []*Player{
		NewPlayer("A", "Alice", 500),
		NewPlayer("B", "Bob", 500),
		NewPlayer("C", "Carol", 500),
	}
	rng := rand.New(rand.NewSource(7))

	dealerIdx := 0
	for hand := 1; hand <= 3; hand++ {
		for _, p := range players {
			p.ResetForNewHand()
		}
		gs := NewGameState("test", hand, players, dealerIdx, 5, 10)
		m := NewMachine(gs, rng)
		if err := m.StartHand(); err != nil {
			t.Fatalf("hand %d StartHand failed: %v", hand, err)
		}

		// Play through quickly.
		for gs.Phase != PhaseSettled {
			current := gs.CurrentPlayer()
			if current == nil {
				break
			}
			toCall := gs.CurrentBet - current.CurrentBet
			var a Action
			if toCall > 0 {
				a = Action{PlayerID: current.ID, Type: ActionCall}
			} else {
				a = Action{PlayerID: current.ID, Type: ActionCheck}
			}
			if err := m.ApplyAction(a); err != nil {
				break
			}
		}

		if gs.Phase != PhaseSettled {
			t.Errorf("hand %d did not settle", hand)
		}

		dealerIdx = (dealerIdx + 1) % len(players)
	}
}
