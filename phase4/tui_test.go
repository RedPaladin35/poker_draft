package tui

import (
	"strings"
	"testing"

	"github.com/p2p-poker/internal/game"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func makeGameState(numPlayers int) *game.GameState {
	players := make([]*game.Player, numPlayers)
	for i := range players {
		id := string(rune('A' + i))
		players[i] = game.NewPlayer(id, "Player "+id, 1000)
	}
	return game.NewGameState("t1", 1, players, 0, 5, 10)
}

// stripANSI removes ANSI escape sequences to make output comparable in tests.
func stripANSI(s string) string {
	// Simple state machine: drop everything between ESC[ and m.
	var out strings.Builder
	skip := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			skip = true
			i++
			continue
		}
		if skip {
			if s[i] == 'm' {
				skip = false
			}
			continue
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

// ── Card rendering tests ──────────────────────────────────────────────────────

func TestRenderCard_ContainsRankAndSuit(t *testing.T) {
	c := game.Card{Rank: game.Ace, Suit: game.Spades}
	rendered := stripANSI(RenderCard(c))
	if !strings.Contains(rendered, "A") {
		t.Errorf("expected rendered card to contain 'A', got: %q", rendered)
	}
	if !strings.Contains(rendered, "♠") {
		t.Errorf("expected rendered card to contain '♠', got: %q", rendered)
	}
}

func TestRenderCard_RedSuitDiffersFromBlack(t *testing.T) {
	red := RenderCard(game.Card{Rank: game.King, Suit: game.Hearts})
	black := RenderCard(game.Card{Rank: game.King, Suit: game.Spades})
	// Both contain K but ANSI escape codes should differ (different colours).
	if red == black {
		t.Error("red-suit card and black-suit card rendered identically")
	}
}

func TestRenderCardBack_DoesNotShowRank(t *testing.T) {
	back := stripANSI(RenderCardBack())
	if !strings.Contains(back, "?") {
		t.Errorf("card back should show '??', got: %q", back)
	}
}

func TestRenderHoleCards_Hidden(t *testing.T) {
	cards := [2]game.Card{
		{Rank: game.Ace, Suit: game.Spades},
		{Rank: game.King, Suit: game.Hearts},
	}
	rendered := stripANSI(RenderHoleCards(cards, false))
	// Should NOT reveal the rank.
	if strings.Contains(rendered, "A") || strings.Contains(rendered, "K") {
		t.Error("hidden hole cards should not show rank")
	}
	if !strings.Contains(rendered, "?") {
		t.Error("hidden hole cards should show '??'")
	}
}

func TestRenderHoleCards_Revealed(t *testing.T) {
	cards := [2]game.Card{
		{Rank: game.Ace, Suit: game.Spades},
		{Rank: game.King, Suit: game.Hearts},
	}
	rendered := stripANSI(RenderHoleCards(cards, true))
	if !strings.Contains(rendered, "A") {
		t.Error("revealed hole cards should show Ace")
	}
	if !strings.Contains(rendered, "K") {
		t.Error("revealed hole cards should show King")
	}
}

func TestRenderHoleCards_Empty(t *testing.T) {
	var cards [2]game.Card // zero value
	rendered := RenderHoleCards(cards, true)
	// Should render without panic, returning placeholders.
	if rendered == "" {
		t.Error("empty hole cards should render placeholders, not empty string")
	}
}

func TestRenderCommunityCards_AllFive(t *testing.T) {
	cards := []game.Card{
		{game.Ace, game.Spades},
		{game.King, game.Hearts},
		{game.Queen, game.Diamonds},
		{game.Jack, game.Clubs},
		{game.Ten, game.Spades},
	}
	rendered := stripANSI(RenderCommunityCards(cards))
	for _, want := range []string{"A", "K", "Q", "J", "10"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("expected %q in community cards, got: %q", want, rendered)
		}
	}
}

func TestRenderCommunityCards_Partial(t *testing.T) {
	// Flop only — turn and river should be placeholders.
	cards := []game.Card{
		{game.Nine, game.Spades},
		{game.Eight, game.Clubs},
		{game.Seven, game.Diamonds},
	}
	// Should not panic with only 3 cards.
	rendered := RenderCommunityCards(cards)
	if rendered == "" {
		t.Error("partial community cards rendered empty string")
	}
}

// ── Player panel tests ────────────────────────────────────────────────────────

func TestRenderPlayerPanel_ShowsName(t *testing.T) {
	p := game.NewPlayer("p1", "Alice", 500)
	rendered := stripANSI(RenderPlayerPanel(p, PlayerPanelOpts{}))
	if !strings.Contains(rendered, "Alice") {
		t.Errorf("player panel should contain player name, got: %q", rendered)
	}
}

func TestRenderPlayerPanel_ShowsStack(t *testing.T) {
	p := game.NewPlayer("p1", "Alice", 750)
	rendered := stripANSI(RenderPlayerPanel(p, PlayerPanelOpts{}))
	if !strings.Contains(rendered, "750") {
		t.Errorf("player panel should show stack 750, got: %q", rendered)
	}
}

func TestRenderPlayerPanel_Nil(t *testing.T) {
	// Nil player should render an empty seat without panicking.
	rendered := RenderPlayerPanel(nil, PlayerPanelOpts{})
	if rendered == "" {
		t.Error("nil player should render empty seat placeholder")
	}
}

func TestRenderPlayerPanel_DealerChip(t *testing.T) {
	p := game.NewPlayer("p1", "Bob", 1000)
	rendered := stripANSI(RenderPlayerPanel(p, PlayerPanelOpts{IsDealer: true}))
	if !strings.Contains(rendered, "D") {
		t.Errorf("dealer panel should show 'D' chip, got: %q", rendered)
	}
}

func TestRenderPlayerPanel_FoldedStatus(t *testing.T) {
	p := game.NewPlayer("p1", "Carol", 1000)
	p.Status = game.StatusFolded
	rendered := stripANSI(RenderPlayerPanel(p, PlayerPanelOpts{}))
	if !strings.Contains(rendered, "fold") {
		t.Errorf("folded player panel should show fold status, got: %q", rendered)
	}
}

func TestRenderPlayerPanel_AllInStatus(t *testing.T) {
	p := game.NewPlayer("p1", "Dave", 0)
	p.Status = game.StatusAllIn
	rendered := stripANSI(RenderPlayerPanel(p, PlayerPanelOpts{}))
	if !strings.Contains(rendered, "all-in") {
		t.Errorf("all-in player panel should show all-in status, got: %q", rendered)
	}
}

// ── BetInputState tests ───────────────────────────────────────────────────────

func TestBetInput_NewState(t *testing.T) {
	gs := makeGameState(3)
	gs.CurrentBet = 20
	p := gs.Players[0]
	p.CurrentBet = 10

	s := NewBetInputState(p, gs)
	if s.ToCall != 10 {
		t.Errorf("ToCall: expected 10, got %d", s.ToCall)
	}
	if s.CanCheck {
		t.Error("CanCheck should be false when there's a bet to call")
	}
}

func TestBetInput_CanCheck(t *testing.T) {
	gs := makeGameState(2)
	gs.CurrentBet = 0
	p := gs.Players[0]

	s := NewBetInputState(p, gs)
	if !s.CanCheck {
		t.Error("CanCheck should be true when no bet to call")
	}
}

func TestBetInput_SelectNavigation(t *testing.T) {
	s := BetInputState{Selected: 0}
	s.SelectNext()
	if s.Selected != 1 {
		t.Errorf("SelectNext: expected 1, got %d", s.Selected)
	}
	s.SelectPrev()
	if s.Selected != 0 {
		t.Errorf("SelectPrev: expected 0, got %d", s.Selected)
	}
	// Wrap around.
	s.Selected = 3
	s.SelectNext()
	if s.Selected != 0 {
		t.Errorf("SelectNext wrap: expected 0, got %d", s.Selected)
	}
	s.Selected = 0
	s.SelectPrev()
	if s.Selected != 3 {
		t.Errorf("SelectPrev wrap: expected 3, got %d", s.Selected)
	}
}

func TestBetInput_Fold(t *testing.T) {
	s := BetInputState{Selected: 0, CanCheck: false, ToCall: 10, Stack: 500}
	action, err := s.Confirm()
	if err != "" {
		t.Fatalf("unexpected error: %s", err)
	}
	if action.Type != game.ActionFold {
		t.Errorf("expected Fold, got %v", action.Type)
	}
}

func TestBetInput_Check(t *testing.T) {
	s := BetInputState{Selected: 1, CanCheck: true, Stack: 500}
	action, err := s.Confirm()
	if err != "" {
		t.Fatalf("unexpected error: %s", err)
	}
	if action.Type != game.ActionCheck {
		t.Errorf("expected Check, got %v", action.Type)
	}
}

func TestBetInput_Call(t *testing.T) {
	s := BetInputState{Selected: 1, CanCheck: false, ToCall: 20, Stack: 500}
	action, err := s.Confirm()
	if err != "" {
		t.Fatalf("unexpected error: %s", err)
	}
	if action.Type != game.ActionCall {
		t.Errorf("expected Call, got %v", action.Type)
	}
}

func TestBetInput_RaiseValid(t *testing.T) {
	s := BetInputState{
		Selected:   2,
		MinRaise:   10,
		ToCall:     5,
		Stack:      500,
		RaiseInput: "30",
	}
	action, err := s.Confirm()
	if err != "" {
		t.Fatalf("unexpected error: %s", err)
	}
	if action.Type != game.ActionRaise {
		t.Errorf("expected Raise, got %v", action.Type)
	}
	if action.Amount != 30 {
		t.Errorf("expected amount 30, got %d", action.Amount)
	}
}

func TestBetInput_RaiseBelowMin(t *testing.T) {
	s := BetInputState{
		Selected:   2,
		MinRaise:   20,
		ToCall:     5,
		Stack:      500,
		RaiseInput: "5",
	}
	_, err := s.Confirm()
	if err == "" {
		t.Error("expected error for raise below minimum")
	}
}

func TestBetInput_RaiseNotEnoughChips(t *testing.T) {
	s := BetInputState{
		Selected:   2,
		MinRaise:   10,
		ToCall:     5,
		Stack:      20,
		RaiseInput: "100",
	}
	_, err := s.Confirm()
	if err == "" {
		t.Error("expected error for insufficient chips")
	}
}

func TestBetInput_AllIn(t *testing.T) {
	s := BetInputState{Selected: 3, Stack: 200}
	action, err := s.Confirm()
	if err != "" {
		t.Fatalf("unexpected error: %s", err)
	}
	if action.Type != game.ActionAllIn {
		t.Errorf("expected AllIn, got %v", action.Type)
	}
}

func TestBetInput_AppendAndBackspace(t *testing.T) {
	s := BetInputState{RaiseInput: "10"}
	s.AppendChar('5')
	if s.RaiseInput != "105" {
		t.Errorf("expected '105', got %q", s.RaiseInput)
	}
	s.Backspace()
	if s.RaiseInput != "10" {
		t.Errorf("expected '10', got %q", s.RaiseInput)
	}
	// Non-digit should be ignored.
	s.AppendChar('x')
	if s.RaiseInput != "10" {
		t.Errorf("non-digit should be ignored, got %q", s.RaiseInput)
	}
}

// ── LogView tests ─────────────────────────────────────────────────────────────

func TestLogView_AddAndRender(t *testing.T) {
	lv := NewLogView()
	lv.Add(LogKindAction, "Alice folds")
	lv.Add(LogKindSystem, "── FLOP ──")
	lv.Add(LogKindWinner, "Bob wins $200")

	if lv.Len() != 3 {
		t.Errorf("expected 3 entries, got %d", lv.Len())
	}

	rendered := stripANSI(lv.Render())
	if !strings.Contains(rendered, "Alice folds") {
		t.Error("log should contain 'Alice folds'")
	}
	if !strings.Contains(rendered, "Bob wins") {
		t.Error("log should contain 'Bob wins'")
	}
}

func TestLogView_AutoScroll(t *testing.T) {
	lv := NewLogView()
	lv.MaxVisible = 3

	for i := 0; i < 10; i++ {
		lv.Add(LogKindSystem, "entry")
	}

	// ScrollTop should have moved so we see the last 3 entries.
	if lv.ScrollTop != 7 {
		t.Errorf("expected ScrollTop=7, got %d", lv.ScrollTop)
	}
}

func TestLogView_ScrollUpDown(t *testing.T) {
	lv := NewLogView()
	lv.MaxVisible = 3
	for i := 0; i < 8; i++ {
		lv.Add(LogKindSystem, "entry")
	}

	originalTop := lv.ScrollTop
	lv.ScrollUp()
	if lv.ScrollTop >= originalTop {
		t.Error("ScrollUp should decrease ScrollTop")
	}
	lv.ScrollDown()
	if lv.ScrollTop != originalTop {
		t.Error("ScrollDown should restore ScrollTop after one ScrollUp")
	}
}

func TestLogView_AddAction(t *testing.T) {
	lv := NewLogView()
	a := game.Action{Type: game.ActionRaise, Amount: 100}
	lv.AddAction("Alice", a)

	if lv.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", lv.Len())
	}
	rendered := stripANSI(lv.Render())
	if !strings.Contains(rendered, "Alice") {
		t.Error("log should mention player name")
	}
	if !strings.Contains(rendered, "100") {
		t.Error("log should mention raise amount")
	}
}

// ── Model tests ───────────────────────────────────────────────────────────────

func TestModel_Init(t *testing.T) {
	m := NewModel("player-1", nil)
	if m.Mode != ModeLobby {
		t.Errorf("initial mode should be ModeLobby, got %v", m.Mode)
	}
	if m.Log == nil {
		t.Error("Log should be initialised")
	}
}

func TestModel_GameStateMsg_SwitchesToBetting(t *testing.T) {
	m := NewModel("A", nil)

	gs := makeGameState(2)
	gs.Phase = game.PhasePreFlop
	gs.ActionIdx = 0 // player A acts first

	newModel, _ := m.Update(GameStateMsg{State: gs})
	m2 := newModel.(Model)

	if m2.Mode != ModeBetting {
		t.Errorf("expected ModeBetting when local player acts, got %v", m2.Mode)
	}
}

func TestModel_GameStateMsg_SwitchesToSpectate(t *testing.T) {
	m := NewModel("A", nil)

	gs := makeGameState(3)
	gs.Phase = game.PhasePreFlop
	gs.ActionIdx = 1 // player B acts, not us

	newModel, _ := m.Update(GameStateMsg{State: gs})
	m2 := newModel.(Model)

	if m2.Mode != ModeSpectate {
		t.Errorf("expected ModeSpectate when another player acts, got %v", m2.Mode)
	}
}

func TestModel_GameStateMsg_PhaseTransitionLogged(t *testing.T) {
	m := NewModel("A", nil)

	gs := makeGameState(2)
	gs.Phase = game.PhaseFlop

	newModel, _ := m.Update(GameStateMsg{State: gs})
	m2 := newModel.(Model)

	if m2.Log.Len() == 0 {
		t.Error("phase transition should be logged")
	}
}

func TestModel_WinnerMsg(t *testing.T) {
	m := NewModel("A", nil)
	m.GameState = makeGameState(2)

	newModel, _ := m.Update(WinnerMsg{
		WinnerIDs: map[string]bool{"A": true},
		HandRanks: map[string]string{"A": "Full House"},
		Payouts:   map[string]int64{"A": 200},
	})
	m2 := newModel.(Model)

	if m2.Mode != ModeShowdown {
		t.Errorf("expected ModeShowdown after WinnerMsg, got %v", m2.Mode)
	}
	if !m2.WinnerIDs["A"] {
		t.Error("winner should be marked")
	}
}

func TestModel_ErrorMsg(t *testing.T) {
	m := NewModel("A", nil)

	newModel, _ := m.Update(ErrorMsg{Text: "connection lost"})
	m2 := newModel.(Model)

	if m2.Mode != ModeError {
		t.Errorf("expected ModeError, got %v", m2.Mode)
	}
	if m2.ErrorText == "" {
		t.Error("ErrorText should be set")
	}
}

func TestModel_View_DoesNotPanic(t *testing.T) {
	// View must never panic regardless of state.
	m := NewModel("A", nil)

	// Lobby mode.
	_ = m.View()

	// With game state.
	m.GameState = makeGameState(4)
	m.Mode = ModeSpectate
	_ = m.View()

	// Betting mode.
	m.Mode = ModeBetting
	m.BetInput = NewBetInputState(m.GameState.Players[0], m.GameState)
	_ = m.View()

	// Showdown mode.
	m.Mode = ModeShowdown
	m.WinnerIDs = map[string]bool{"A": true}
	_ = m.View()

	// Error mode.
	m.Mode = ModeError
	m.ErrorText = "test error"
	_ = m.View()
}

func TestModel_ActionSubmission(t *testing.T) {
	var received game.Action
	m := NewModel("A", func(a game.Action) {
		received = a
	})

	gs := makeGameState(2)
	gs.Phase = game.PhasePreFlop
	gs.ActionIdx = 0
	m.GameState = gs
	m.Mode = ModeBetting
	m.BetInput = BetInputState{
		Selected: 0, // fold
		Stack:    1000,
	}

	// Simulate pressing Enter to confirm fold.
	import_tea_key := struct{ Type int }{} // placeholder — actual key handled via string
	_ = import_tea_key

	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 := newModel.(Model)

	if received.Type != game.ActionFold {
		t.Errorf("expected Fold action, got %v", received.Type)
	}
	if m2.Mode != ModeSpectate {
		t.Errorf("expected ModeSpectate after action, got %v", m2.Mode)
	}
}

// ── truncate helper test ──────────────────────────────────────────────────────

func TestTruncate(t *testing.T) {
	if got := truncate("Hello", 10); got != "Hello" {
		t.Errorf("short string should not be truncated: %q", got)
	}
	if got := truncate("Hello World!", 8); !strings.HasSuffix(got, "…") {
		t.Errorf("long string should end with ellipsis: %q", got)
	}
	if len([]rune(truncate("Hello World!", 8))) > 8 {
		t.Error("truncated string should not exceed max length")
	}
}
