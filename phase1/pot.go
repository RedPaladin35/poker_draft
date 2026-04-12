package game

import "sort"

// PotSlice holds one pot (main or side) and the set of players eligible to win it.
type PotSlice struct {
	Amount      int64
	EligibleIDs []string // player IDs who can win this pot
}

// PotManager calculates main pot and all side pots from per-player total bets.
//
// Texas Hold'em side-pot rules:
//   - A player who goes all-in can only win, from each opponent, as much as they put in.
//   - Multiple all-in players at different stack levels create cascading side pots.
//
// Algorithm:
//  1. Sort players by TotalBet ascending (smallest all-in first).
//  2. For each distinct bet level, create a pot slice containing:
//     - The amount each eligible player contributed up to this level.
//     - All players whose TotalBet >= this level are eligible.
//  3. Remove players whose TotalBet cap has been exhausted from subsequent pots.
func CalculatePots(players []*Player) []PotSlice {
	// Collect only players who contributed something.
	type contrib struct {
		player   *Player
		totalBet int64
	}
	var contribs []contrib
	for _, p := range players {
		if p.TotalBet > 0 {
			contribs = append(contribs, contrib{p, p.TotalBet})
		}
	}
	if len(contribs) == 0 {
		return nil
	}

	// Sort by ascending contribution.
	sort.Slice(contribs, func(i, j int) bool {
		return contribs[i].totalBet < contribs[j].totalBet
	})

	var pots []PotSlice
	prevLevel := int64(0)

	for i, c := range contribs {
		if c.totalBet == prevLevel {
			// This player contributed the same as the previous — they are already
			// accounted for; just remove them from future eligibility.
			continue
		}
		level := c.totalBet
		diff := level - prevLevel

		// All remaining contribs (from index i onward) contribute up to this level.
		var amount int64
		var eligible []string
		for _, rc := range contribs[i:] {
			amount += diff
			eligible = append(eligible, rc.player.ID)
		}
		// Also add players from earlier levels who are still eligible (they folded
		// or went all-in at a lower amount but are still owed their pot slice
		// from others who matched up to this level). These were already captured
		// in earlier pot slices, so we don't re-add them here — they simply cannot
		// win the current pot beyond their cap.

		pots = append(pots, PotSlice{
			Amount:      amount,
			EligibleIDs: eligible,
		})
		prevLevel = level
	}

	// Merge consecutive pots with identical eligible sets to reduce noise
	// (common when no all-ins occur — results in one clean main pot).
	return mergePots(pots)
}

func mergePots(pots []PotSlice) []PotSlice {
	if len(pots) == 0 {
		return pots
	}
	merged := []PotSlice{pots[0]}
	for _, p := range pots[1:] {
		last := &merged[len(merged)-1]
		if sameEligible(last.EligibleIDs, p.EligibleIDs) {
			last.Amount += p.Amount
		} else {
			merged = append(merged, p)
		}
	}
	return merged
}

func sameEligible(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, id := range a {
		set[id] = struct{}{}
	}
	for _, id := range b {
		if _, ok := set[id]; !ok {
			return false
		}
	}
	return true
}

// TotalPot returns the sum of all pot slices — useful for display.
func TotalPot(pots []PotSlice) int64 {
	var total int64
	for _, p := range pots {
		total += p.Amount
	}
	return total
}
