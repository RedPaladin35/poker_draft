package game

import "fmt"

// PlayerStatus represents a player's current state within a hand.
type PlayerStatus uint8

const (
	StatusActive    PlayerStatus = iota // still in the hand
	StatusFolded                        // folded this hand
	StatusAllIn                         // all chips in, cannot act further
	StatusSittingOut                    // sitting out (disconnected / waiting)
)

func (s PlayerStatus) String() string {
	return [...]string{"Active", "Folded", "All-In", "Sitting Out"}[s]
}

// Player represents a single participant at the table.
type Player struct {
	ID         string       // unique peer ID (PeerID in Phase 3)
	Name       string       // display name
	Stack      int64        // chip stack in the smallest denomination
	HoleCards  [2]Card      // private hole cards (zero value before deal)
	Status     PlayerStatus
	CurrentBet int64 // amount bet in the current betting round
	TotalBet   int64 // total amount bet across all rounds this hand (for side-pot math)
}

// NewPlayer constructs a player with a given ID, name, and starting stack.
func NewPlayer(id, name string, stack int64) *Player {
	return &Player{
		ID:     id,
		Name:   name,
		Stack:  stack,
		Status: StatusActive,
	}
}

// IsActive returns true if the player can still act.
func (p *Player) IsActive() bool {
	return p.Status == StatusActive
}

// CanAct returns true if the player still makes decisions (not folded/all-in/sitting out).
func (p *Player) CanAct() bool {
	return p.Status == StatusActive
}

// PlaceBet deducts amount from the player's stack and records the bet.
// Returns the actual amount placed (may be less if player goes all-in).
func (p *Player) PlaceBet(amount int64) int64 {
	if amount >= p.Stack {
		amount = p.Stack
		p.Status = StatusAllIn
	}
	p.Stack -= amount
	p.CurrentBet += amount
	p.TotalBet += amount
	return amount
}

// ResetForNewHand clears per-hand state.
func (p *Player) ResetForNewHand() {
	p.HoleCards = [2]Card{}
	p.Status = StatusActive
	p.CurrentBet = 0
	p.TotalBet = 0
}

// ResetForNewRound clears the current-round bet tracker (called between streets).
func (p *Player) ResetForNewRound() {
	p.CurrentBet = 0
}

func (p *Player) String() string {
	return fmt.Sprintf("%s(%s) stack=%d status=%s", p.Name, p.ID, p.Stack, p.Status)
}
