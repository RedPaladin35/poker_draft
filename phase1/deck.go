package game

import (
	"fmt"
	"math/rand"
)

// Suit represents a card suit.
type Suit uint8

const (
	Spades Suit = iota
	Hearts
	Diamonds
	Clubs
)

func (s Suit) String() string {
	return [...]string{"♠", "♥", "♦", "♣"}[s]
}

// Rank represents a card rank (2–14, where 14 = Ace).
type Rank uint8

const (
	Two Rank = iota + 2
	Three
	Four
	Five
	Six
	Seven
	Eight
	Nine
	Ten
	Jack
	Queen
	King
	Ace
)

func (r Rank) String() string {
	switch r {
	case Two, Three, Four, Five, Six, Seven, Eight, Nine, Ten:
		return fmt.Sprintf("%d", int(r))
	case Jack:
		return "J"
	case Queen:
		return "Q"
	case King:
		return "K"
	case Ace:
		return "A"
	}
	return "?"
}

// Card is a standard playing card.
type Card struct {
	Rank Rank
	Suit Suit
}

func (c Card) String() string {
	return c.Rank.String() + c.Suit.String()
}

// CardID returns a unique integer 0–51 for the card.
func (c Card) CardID() int {
	return int(c.Suit)*13 + int(c.Rank-2)
}

// CardFromID reconstructs a Card from its ID.
func CardFromID(id int) Card {
	return Card{
		Suit: Suit(id / 13),
		Rank: Rank(id%13) + 2,
	}
}

// Deck holds an ordered slice of cards.
type Deck struct {
	Cards []Card
}

// NewDeck returns a full, ordered 52-card deck.
func NewDeck() *Deck {
	d := &Deck{}
	for s := Spades; s <= Clubs; s++ {
		for r := Two; r <= Ace; r++ {
			d.Cards = append(d.Cards, Card{Rank: r, Suit: s})
		}
	}
	return d
}

// Shuffle randomises the deck in place using a provided source of randomness.
// In the real protocol this will be replaced by Mental Poker shuffling.
func (d *Deck) Shuffle(rng *rand.Rand) {
	rng.Shuffle(len(d.Cards), func(i, j int) {
		d.Cards[i], d.Cards[j] = d.Cards[j], d.Cards[i]
	})
}

// Deal removes and returns the top card.
func (d *Deck) Deal() (Card, error) {
	if len(d.Cards) == 0 {
		return Card{}, fmt.Errorf("deck is empty")
	}
	c := d.Cards[0]
	d.Cards = d.Cards[1:]
	return c, nil
}

// Remaining returns the number of cards left in the deck.
func (d *Deck) Remaining() int {
	return len(d.Cards)
}
