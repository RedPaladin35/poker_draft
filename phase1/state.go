package game

import "fmt"

// Phase represents the current stage of a poker hand.
type Phase uint8

const (
	PhaseWaiting  Phase = iota // table not yet full / ready
	PhasePreFlop               // hole cards dealt, pre-flop betting
	PhaseFlop                  // 3 community cards, betting
	PhaseTurn                  // 4th community card, betting
	PhaseRiver                 // 5th community card, betting
	PhaseShowdown              // reveal hands, determine winners
	PhaseSettled               // payouts done, ready for next hand
)

func (p Phase) String() string {
	return [...]string{
		"Waiting", "Pre-Flop", "Flop", "Turn", "River", "Showdown", "Settled",
	}[p]
}

// ActionType enumerates the actions a player may take.
type ActionType uint8

const (
	ActionFold  ActionType = iota
	ActionCheck            // only valid when no bet to call
	ActionCall             // match the current bet
	ActionRaise            // increase the current bet
	ActionAllIn            // go all-in (shorthand for raise to full stack)
)

func (a ActionType) String() string {
	return [...]string{"Fold", "Check", "Call", "Raise", "All-In"}[a]
}

// Action is a concrete move by a player.
type Action struct {
	PlayerID string
	Type     ActionType
	Amount   int64 // only meaningful for Raise / AllIn
}

// GameState is the complete, deterministic state of a single hand.
// It is updated by ApplyAction and by the state machine in machine.go.
type GameState struct {
	// Table-level metadata.
	TableID   string
	HandNum   int
	SmallBlind int64
	BigBlind   int64

	// Player list in seat order. Index 0 = seat 0.
	Players []*Player

	// Positional indices (into Players slice).
	DealerIdx    int
	ActionIdx    int // whose turn it is
	LastRaiserIdx int // index of the last player to raise (for re-raise detection)

	// Current phase.
	Phase Phase

	// Community cards (0–5 cards depending on phase).
	CommunityCards []Card

	// Deck (replaced by Mental Poker in Phase 2).
	Deck *Deck

	// Pots (recalculated at end of each betting round).
	Pots []PotSlice

	// Per-round tracking.
	CurrentBet int64 // highest bet in this round that others must match
	MinRaise   int64 // minimum legal raise increment (= last raise size or big blind)
	RoundActionCount int // number of actions taken in the current betting round

	// Hand log — every action appended in order.
	Log []Action

	// Final payouts: playerID -> net chip change. Populated at Settled.
	Payouts map[string]int64
}

// NewGameState initialises a fresh GameState for a new hand.
func NewGameState(tableID string, handNum int, players []*Player, dealerIdx int, sb, bb int64) *GameState {
	gs := &GameState{
		TableID:    tableID,
		HandNum:    handNum,
		SmallBlind: sb,
		BigBlind:   bb,
		Players:    players,
		DealerIdx:  dealerIdx,
		Phase:      PhaseWaiting,
		Deck:       NewDeck(),
		Payouts:    make(map[string]int64),
		MinRaise:   bb,
	}
	for _, p := range players {
		p.ResetForNewHand()
	}
	return gs
}

// ActivePlayers returns players who have not folded, gone all-in, or sat out.
func (gs *GameState) ActivePlayers() []*Player {
	var active []*Player
	for _, p := range gs.Players {
		if p.IsActive() {
			active = append(active, p)
		}
	}
	return active
}

// PlayersInHand returns all players still eligible for pots (active + all-in).
func (gs *GameState) PlayersInHand() []*Player {
	var inHand []*Player
	for _, p := range gs.Players {
		if p.Status == StatusActive || p.Status == StatusAllIn {
			inHand = append(inHand, p)
		}
	}
	return inHand
}

// CurrentPlayer returns the player whose turn it is, or nil.
func (gs *GameState) CurrentPlayer() *Player {
	if gs.ActionIdx < 0 || gs.ActionIdx >= len(gs.Players) {
		return nil
	}
	return gs.Players[gs.ActionIdx]
}

// nextActiveIndex returns the seat index of the next active (CanAct) player
// starting after fromIdx, wrapping around. Returns -1 if none found.
func (gs *GameState) nextActiveIndex(fromIdx int) int {
	n := len(gs.Players)
	for i := 1; i <= n; i++ {
		idx := (fromIdx + i) % n
		if gs.Players[idx].CanAct() {
			return idx
		}
	}
	return -1
}

// SeatIndex returns the seat index for a given player ID, or -1 if not found.
func (gs *GameState) SeatIndex(playerID string) int {
	for i, p := range gs.Players {
		if p.ID == playerID {
			return i
		}
	}
	return -1
}

func (gs *GameState) String() string {
	return fmt.Sprintf("Hand#%d Phase=%s Pot=%d CurrentBet=%d ActivePlayers=%d",
		gs.HandNum, gs.Phase, TotalPot(gs.Pots), gs.CurrentBet, len(gs.ActivePlayers()))
}
