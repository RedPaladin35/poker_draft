package game

import (
	"fmt"
	"math/rand"
)

// Machine drives the Texas Hold'em state machine.
// It wraps a GameState and exposes methods for phase transitions and player actions.
type Machine struct {
	State *GameState
	rng   *rand.Rand
}

// NewMachine creates a Machine ready to start a new hand.
func NewMachine(gs *GameState, rng *rand.Rand) *Machine {
	return &Machine{State: gs, rng: rng}
}

// StartHand transitions from PhaseWaiting to PhasePreFlop:
// shuffles the deck, posts blinds, deals hole cards, and sets the first actor.
func (m *Machine) StartHand() error {
	if m.State.Phase != PhaseWaiting {
		return fmt.Errorf("StartHand: expected PhaseWaiting, got %s", m.State.Phase)
	}
	if len(m.State.Players) < 2 {
		return fmt.Errorf("StartHand: need at least 2 players, have %d", len(m.State.Players))
	}

	m.State.Deck.Shuffle(m.rng)

	if err := m.postBlinds(); err != nil {
		return err
	}
	if err := m.dealHoleCards(); err != nil {
		return err
	}

	// Pre-flop action starts left of the big blind.
	bbIdx := m.bigBlindIndex()
	m.State.ActionIdx = m.State.nextActiveIndex(bbIdx)
	m.State.LastRaiserIdx = bbIdx // BB is treated as the last raiser pre-flop
	m.State.RoundActionCount = 0
	m.State.Phase = PhasePreFlop
	return nil
}

// ApplyAction validates and applies a player action. Returns an error if invalid.
// After a successful action it advances the action pointer and, if the betting
// round is complete, transitions to the next phase automatically.
func (m *Machine) ApplyAction(a Action) error {
	gs := m.State

	if gs.Phase == PhaseShowdown || gs.Phase == PhaseSettled || gs.Phase == PhaseWaiting {
		return fmt.Errorf("ApplyAction: no actions allowed in phase %s", gs.Phase)
	}

	current := gs.CurrentPlayer()
	if current == nil || current.ID != a.PlayerID {
		return fmt.Errorf("ApplyAction: it is not %s's turn (current: %v)", a.PlayerID, current)
	}
	if !current.CanAct() {
		return fmt.Errorf("ApplyAction: player %s cannot act (status: %s)", a.PlayerID, current.Status)
	}

	switch a.Type {
	case ActionFold:
		current.Status = StatusFolded

	case ActionCheck:
		toCall := gs.CurrentBet - current.CurrentBet
		if toCall != 0 {
			return fmt.Errorf("ApplyAction: cannot check with a bet of %d to call", toCall)
		}
		// nothing to do, just advance

	case ActionCall:
		toCall := gs.CurrentBet - current.CurrentBet
		if toCall <= 0 {
			return fmt.Errorf("ApplyAction: nothing to call (use Check)")
		}
		current.PlaceBet(toCall)

	case ActionRaise:
		toCall := gs.CurrentBet - current.CurrentBet
		totalNeeded := toCall + a.Amount
		if a.Amount < gs.MinRaise {
			return fmt.Errorf("ApplyAction: raise of %d is below minimum %d", a.Amount, gs.MinRaise)
		}
		if totalNeeded > current.Stack+current.CurrentBet {
			return fmt.Errorf("ApplyAction: insufficient stack for raise")
		}
		gs.MinRaise = a.Amount
		gs.CurrentBet += a.Amount
		current.PlaceBet(totalNeeded)
		gs.LastRaiserIdx = gs.ActionIdx

	case ActionAllIn:
		// Player bets everything they have.
		allin := current.Stack
		total := current.CurrentBet + allin
		if total > gs.CurrentBet {
			// This all-in constitutes a raise.
			raise := total - gs.CurrentBet
			if raise > gs.MinRaise {
				gs.MinRaise = raise
			}
			gs.CurrentBet = total
			gs.LastRaiserIdx = gs.ActionIdx
		}
		current.PlaceBet(allin)

	default:
		return fmt.Errorf("ApplyAction: unknown action type %d", a.Type)
	}

	gs.Log = append(gs.Log, a)
	gs.RoundActionCount++

	// Check if the hand is over due to all-but-one folding.
	if m.onlyOneRemaining() {
		return m.resolveSingleWinner()
	}

	// Advance to next actor or end the betting round.
	return m.advanceAction()
}

// advanceAction moves ActionIdx to the next player who can act,
// or closes the betting round if everyone has acted and bets are equal.
func (m *Machine) advanceAction() error {
	gs := m.State
	nextIdx := gs.nextActiveIndex(gs.ActionIdx)

	// If nobody else can act (all others folded/all-in), close the round.
	if nextIdx == -1 || !gs.Players[nextIdx].CanAct() {
		return m.endBettingRound()
	}

	// Check if the round is complete:
	// Every active player has acted at least once AND bets are equalised.
	if m.bettingRoundComplete(nextIdx) {
		return m.endBettingRound()
	}

	gs.ActionIdx = nextIdx
	return nil
}

// bettingRoundComplete returns true when nextIdx has already acted and is not
// owed a chance to re-raise (i.e., their bet equals CurrentBet).
func (m *Machine) bettingRoundComplete(nextIdx int) bool {
	gs := m.State
	next := gs.Players[nextIdx]

	// The next player to act still has an obligation to call or has not acted.
	if next.CurrentBet < gs.CurrentBet {
		return false
	}
	// If a raise happened, everyone must get another chance — check if we
	// have gone all the way around back to the raiser.
	if gs.RoundActionCount > 0 && nextIdx == gs.LastRaiserIdx {
		// We've come full circle to the last raiser — round is done.
		// Exception: pre-flop big blind gets one more action if nobody raised.
		return true
	}
	// If at least one full orbit has completed with no raises, we're done.
	active := m.countCanAct()
	return gs.RoundActionCount >= active
}

func (m *Machine) countCanAct() int {
	n := 0
	for _, p := range m.State.Players {
		if p.CanAct() {
			n++
		}
	}
	return n
}

// endBettingRound collects bets into pots and advances to the next phase.
func (m *Machine) endBettingRound() error {
	gs := m.State
	// Recalculate pots including this round's bets.
	gs.Pots = CalculatePots(gs.Players)

	switch gs.Phase {
	case PhasePreFlop:
		return m.dealFlop()
	case PhaseFlop:
		return m.dealTurn()
	case PhaseTurn:
		return m.dealRiver()
	case PhaseRiver:
		return m.startShowdown()
	}
	return nil
}

func (m *Machine) dealFlop() error {
	gs := m.State
	// Burn one card (standard poker dealing protocol).
	if _, err := gs.Deck.Deal(); err != nil {
		return err
	}
	for i := 0; i < 3; i++ {
		c, err := gs.Deck.Deal()
		if err != nil {
			return err
		}
		gs.CommunityCards = append(gs.CommunityCards, c)
	}
	gs.Phase = PhaseFlop
	return m.startNewBettingRound()
}

func (m *Machine) dealTurn() error {
	gs := m.State
	if _, err := gs.Deck.Deal(); err != nil { // burn
		return err
	}
	c, err := gs.Deck.Deal()
	if err != nil {
		return err
	}
	gs.CommunityCards = append(gs.CommunityCards, c)
	gs.Phase = PhaseTurn
	return m.startNewBettingRound()
}

func (m *Machine) dealRiver() error {
	gs := m.State
	if _, err := gs.Deck.Deal(); err != nil { // burn
		return err
	}
	c, err := gs.Deck.Deal()
	if err != nil {
		return err
	}
	gs.CommunityCards = append(gs.CommunityCards, c)
	gs.Phase = PhaseRiver
	return m.startNewBettingRound()
}

// startNewBettingRound resets per-round tracking and sets the first actor
// to the first active player left of the dealer.
func (m *Machine) startNewBettingRound() error {
	gs := m.State
	gs.CurrentBet = 0
	gs.MinRaise = gs.BigBlind
	gs.RoundActionCount = 0
	gs.LastRaiserIdx = -1

	for _, p := range gs.Players {
		if p.Status == StatusActive || p.Status == StatusAllIn {
			p.ResetForNewRound()
		}
	}

	first := gs.nextActiveIndex(gs.DealerIdx)
	if first == -1 {
		// No active players — go straight to showdown.
		return m.startShowdown()
	}
	gs.ActionIdx = first

	// If only one player can act (rest all-in), skip betting and run the board.
	if m.countCanAct() <= 1 {
		return m.endBettingRound()
	}
	return nil
}

// postBlinds posts small blind (seat left of dealer) and big blind (next seat).
func (m *Machine) postBlinds() error {
	gs := m.State
	n := len(gs.Players)

	sbIdx := (gs.DealerIdx + 1) % n
	bbIdx := (gs.DealerIdx + 2) % n

	// Heads-up rule: dealer posts SB, other player posts BB.
	if n == 2 {
		sbIdx = gs.DealerIdx
		bbIdx = (gs.DealerIdx + 1) % n
	}

	sb := gs.Players[sbIdx]
	bb := gs.Players[bbIdx]

	sbAmount := sb.PlaceBet(gs.SmallBlind)
	bbAmount := bb.PlaceBet(gs.BigBlind)

	gs.CurrentBet = bbAmount
	if sbAmount > gs.CurrentBet {
		gs.CurrentBet = sbAmount
	}

	gs.Log = append(gs.Log, Action{PlayerID: sb.ID, Type: ActionRaise, Amount: sbAmount})
	gs.Log = append(gs.Log, Action{PlayerID: bb.ID, Type: ActionRaise, Amount: bbAmount})
	return nil
}

// dealHoleCards deals 2 cards to every player in seat order.
func (m *Machine) dealHoleCards() error {
	gs := m.State
	n := len(gs.Players)
	start := (gs.DealerIdx + 1) % n
	for round := 0; round < 2; round++ {
		for i := 0; i < n; i++ {
			idx := (start + i) % n
			c, err := gs.Deck.Deal()
			if err != nil {
				return fmt.Errorf("dealHoleCards: %w", err)
			}
			gs.Players[idx].HoleCards[round] = c
		}
	}
	return nil
}

// smallBlindIndex returns the seat index of the small blind.
func (m *Machine) bigBlindIndex() int {
	n := len(m.State.Players)
	if n == 2 {
		return (m.State.DealerIdx + 1) % n
	}
	return (m.State.DealerIdx + 2) % n
}

// onlyOneRemaining returns true if at most one player has not folded.
func (m *Machine) onlyOneRemaining() bool {
	count := 0
	for _, p := range m.State.Players {
		if p.Status != StatusFolded && p.Status != StatusSittingOut {
			count++
		}
	}
	return count <= 1
}

// resolveSingleWinner awards the entire pot to the last player standing.
func (m *Machine) resolveSingleWinner() error {
	gs := m.State
	gs.Pots = CalculatePots(gs.Players)
	total := TotalPot(gs.Pots)
	for _, p := range gs.Players {
		if p.Status != StatusFolded && p.Status != StatusSittingOut {
			p.Stack += total
			gs.Payouts[p.ID] += total
			break
		}
	}
	gs.Phase = PhaseSettled
	return nil
}

// startShowdown reveals all hands and distributes pots.
func (m *Machine) startShowdown() error {
	gs := m.State
	gs.Phase = PhaseShowdown
	gs.Pots = CalculatePots(gs.Players)
	return m.distributePots()
}

// distributePots awards each pot to its winner(s) using hand evaluation.
// Handles split pots when hands are equal.
func (m *Machine) distributePots() error {
	gs := m.State

	// Build community 5-card array.
	comm := gs.CommunityCards

	// For each pot, find the best hand among eligible players.
	for _, pot := range gs.Pots {
		winners := m.potWinners(pot, comm)
		if len(winners) == 0 {
			continue
		}
		share := pot.Amount / int64(len(winners))
		remainder := pot.Amount % int64(len(winners))
		for _, w := range winners {
			w.Stack += share
			gs.Payouts[w.ID] += share
		}
		// Remainder chip goes to the player closest left of the dealer.
		if remainder > 0 {
			closest := m.closestLeftOfDealer(winners)
			closest.Stack += remainder
			gs.Payouts[closest.ID] += remainder
		}
	}

	gs.Phase = PhaseSettled
	return nil
}

// potWinners returns the player(s) with the best hand among those eligible.
func (m *Machine) potWinners(pot PotSlice, comm []Card) []*Player {
	gs := m.State

	type entry struct {
		player *Player
		hand   EvaluatedHand
	}

	var candidates []entry
	for _, pid := range pot.EligibleIDs {
		idx := gs.SeatIndex(pid)
		if idx == -1 {
			continue
		}
		p := gs.Players[idx]
		if p.Status == StatusFolded {
			continue
		}

		var seven [7]Card
		seven[0] = p.HoleCards[0]
		seven[1] = p.HoleCards[1]
		for i, c := range comm {
			if i+2 < 7 {
				seven[i+2] = c
			}
		}
		candidates = append(candidates, entry{p, EvaluateBest7(seven)})
	}

	if len(candidates) == 0 {
		return nil
	}

	best := candidates[0].hand
	for _, e := range candidates[1:] {
		if e.hand.Compare(best) > 0 {
			best = e.hand
		}
	}

	var winners []*Player
	for _, e := range candidates {
		if e.hand.Compare(best) == 0 {
			winners = append(winners, e.player)
		}
	}
	return winners
}

// closestLeftOfDealer returns the winner closest left of the dealer (for
// odd-chip allocation).
func (m *Machine) closestLeftOfDealer(winners []*Player) *Player {
	gs := m.State
	n := len(gs.Players)
	winSet := make(map[string]*Player, len(winners))
	for _, w := range winners {
		winSet[w.ID] = w
	}
	for i := 1; i <= n; i++ {
		idx := (gs.DealerIdx + i) % n
		if p, ok := winSet[gs.Players[idx].ID]; ok {
			return p
		}
	}
	return winners[0]
}
