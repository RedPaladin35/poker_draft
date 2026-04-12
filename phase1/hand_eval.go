package game

import "sort"

// HandRank is an ordered category — higher is better.
type HandRank uint8

const (
	HighCard HandRank = iota
	OnePair
	TwoPair
	ThreeOfAKind
	Straight
	Flush
	FullHouse
	FourOfAKind
	StraightFlush
	RoyalFlush
)

func (h HandRank) String() string {
	return [...]string{
		"High Card", "One Pair", "Two Pair", "Three of a Kind",
		"Straight", "Flush", "Full House", "Four of a Kind",
		"Straight Flush", "Royal Flush",
	}[h]
}

// EvaluatedHand holds the best 5-card hand found in a 7-card set.
type EvaluatedHand struct {
	Rank    HandRank
	Cards   [5]Card  // best 5-card combination
	Kickers []Rank   // tiebreaker ranks in descending order
}

// Compare returns 1 if h beats other, -1 if other beats h, 0 on a tie.
func (h EvaluatedHand) Compare(other EvaluatedHand) int {
	if h.Rank > other.Rank {
		return 1
	}
	if h.Rank < other.Rank {
		return -1
	}
	// Same category — compare kickers.
	for i := 0; i < len(h.Kickers) && i < len(other.Kickers); i++ {
		if h.Kickers[i] > other.Kickers[i] {
			return 1
		}
		if h.Kickers[i] < other.Kickers[i] {
			return -1
		}
	}
	return 0
}

// EvaluateBest7 evaluates all C(7,5)=21 five-card combinations from 7 cards
// and returns the best EvaluatedHand.
func EvaluateBest7(cards [7]Card) EvaluatedHand {
	best := EvaluatedHand{Rank: HighCard}
	// Iterate all pairs of excluded indices (i, j) where 0 <= i < j <= 6.
	// That gives C(7,2) = 21 combinations, each leaving 5 cards.
	for i := 0; i < 7; i++ {
		for j := i + 1; j < 7; j++ {
			var five [5]Card
			k := 0
			for idx := 0; idx < 7; idx++ {
				if idx == i || idx == j {
					continue
				}
				five[k] = cards[idx]
				k++
			}
			h := evaluate5(five)
			if h.Compare(best) > 0 {
				best = h
			}
		}
	}
	return best
}

// evaluate5 determines the rank of exactly 5 cards.
func evaluate5(cards [5]Card) EvaluatedHand {
	// Sort descending by rank.
	sorted := cards
	sort.Slice(sorted[:], func(i, j int) bool {
		return sorted[i].Rank > sorted[j].Rank
	})

	isFlush := checkFlush(sorted)
	isStraight, straightHigh := checkStraight(sorted)

	switch {
	case isFlush && isStraight && straightHigh == Ace:
		return EvaluatedHand{Rank: RoyalFlush, Cards: sorted, Kickers: []Rank{Ace}}
	case isFlush && isStraight:
		return EvaluatedHand{Rank: StraightFlush, Cards: sorted, Kickers: []Rank{straightHigh}}
	}

	groups := groupByRank(sorted)

	switch {
	case groups[0].count == 4:
		return EvaluatedHand{
			Rank:    FourOfAKind,
			Cards:   sorted,
			Kickers: []Rank{groups[0].rank, groups[1].rank},
		}
	case groups[0].count == 3 && groups[1].count == 2:
		return EvaluatedHand{
			Rank:    FullHouse,
			Cards:   sorted,
			Kickers: []Rank{groups[0].rank, groups[1].rank},
		}
	case isFlush:
		kickers := ranksDesc(sorted)
		return EvaluatedHand{Rank: Flush, Cards: sorted, Kickers: kickers}
	case isStraight:
		return EvaluatedHand{Rank: Straight, Cards: sorted, Kickers: []Rank{straightHigh}}
	case groups[0].count == 3:
		return EvaluatedHand{
			Rank:    ThreeOfAKind,
			Cards:   sorted,
			Kickers: []Rank{groups[0].rank, groups[1].rank, groups[2].rank},
		}
	case groups[0].count == 2 && groups[1].count == 2:
		// Two pair — higher pair first, then lower pair, then kicker.
		high, low := groups[0].rank, groups[1].rank
		if low > high {
			high, low = low, high
		}
		return EvaluatedHand{
			Rank:    TwoPair,
			Cards:   sorted,
			Kickers: []Rank{high, low, groups[2].rank},
		}
	case groups[0].count == 2:
		kickers := make([]Rank, 0, 4)
		kickers = append(kickers, groups[0].rank)
		for _, g := range groups[1:] {
			kickers = append(kickers, g.rank)
		}
		return EvaluatedHand{Rank: OnePair, Cards: sorted, Kickers: kickers}
	default:
		return EvaluatedHand{Rank: HighCard, Cards: sorted, Kickers: ranksDesc(sorted)}
	}
}

// rankGroup tracks how many cards share a rank.
type rankGroup struct {
	rank  Rank
	count int
}

// groupByRank returns groups sorted by count desc, then rank desc.
func groupByRank(cards [5]Card) []rankGroup {
	counts := make(map[Rank]int)
	for _, c := range cards {
		counts[c.Rank]++
	}
	groups := make([]rankGroup, 0, len(counts))
	for r, n := range counts {
		groups = append(groups, rankGroup{r, n})
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].count != groups[j].count {
			return groups[i].count > groups[j].count
		}
		return groups[i].rank > groups[j].rank
	})
	return groups
}

func checkFlush(cards [5]Card) bool {
	s := cards[0].Suit
	for _, c := range cards[1:] {
		if c.Suit != s {
			return false
		}
	}
	return true
}

// checkStraight returns whether the 5 sorted cards form a straight,
// and the highest card in the straight (handles A-2-3-4-5 wheel).
func checkStraight(cards [5]Card) (bool, Rank) {
	// Normal straight.
	for i := 1; i < 5; i++ {
		if int(cards[i-1].Rank)-int(cards[i].Rank) != 1 {
			goto wheel
		}
	}
	return true, cards[0].Rank

wheel:
	// Wheel: A-2-3-4-5 (Ace plays as low).
	if cards[0].Rank == Ace &&
		cards[1].Rank == Five &&
		cards[2].Rank == Four &&
		cards[3].Rank == Three &&
		cards[4].Rank == Two {
		return true, Five
	}
	return false, 0
}

func ranksDesc(cards [5]Card) []Rank {
	out := make([]Rank, 5)
	for i, c := range cards {
		out[i] = c.Rank
	}
	return out
}
