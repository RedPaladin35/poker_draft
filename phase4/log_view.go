package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/p2p-poker/internal/game"
)

// LogEntryKind classifies the visual style of a log entry.
type LogEntryKind uint8

const (
	LogKindAction  LogEntryKind = iota // player betting action
	LogKindSystem                      // phase transitions, deal events
	LogKindWinner                      // pot award message
	LogKindError                       // fault / timeout / invalid action
	LogKindNetwork                     // peer join/leave/disconnect
)

// LogEntry is a single line in the action log.
type LogEntry struct {
	Kind      LogEntryKind
	Timestamp time.Time
	Text      string
}

// LogView holds the log entries and scroll position.
type LogView struct {
	Entries    []LogEntry
	ScrollTop  int // index of the first visible entry
	MaxVisible int // how many lines fit (= LogHeight - 2 for borders)
}

// NewLogView creates an empty log.
func NewLogView() *LogView {
	return &LogView{MaxVisible: LogHeight - 2}
}

// Add appends a new entry and auto-scrolls to the bottom.
func (lv *LogView) Add(kind LogEntryKind, text string) {
	lv.Entries = append(lv.Entries, LogEntry{
		Kind:      kind,
		Timestamp: time.Now(),
		Text:      text,
	})
	// Auto-scroll: keep the newest entries visible.
	if len(lv.Entries) > lv.MaxVisible {
		lv.ScrollTop = len(lv.Entries) - lv.MaxVisible
	}
}

// AddAction is a convenience wrapper for player betting actions.
func (lv *LogView) AddAction(playerName string, a game.Action) {
	var text string
	switch a.Type {
	case game.ActionFold:
		text = fmt.Sprintf("%s folds", playerName)
	case game.ActionCheck:
		text = fmt.Sprintf("%s checks", playerName)
	case game.ActionCall:
		text = fmt.Sprintf("%s calls", playerName)
	case game.ActionRaise:
		text = fmt.Sprintf("%s raises $%d", playerName, a.Amount)
	case game.ActionAllIn:
		text = fmt.Sprintf("%s goes ALL-IN", playerName)
	}
	lv.Add(LogKindAction, text)
}

// AddPhase logs a phase transition.
func (lv *LogView) AddPhase(phase game.Phase) {
	lv.Add(LogKindSystem, fmt.Sprintf("── %s ──", strings.ToUpper(phase.String())))
}

// AddWinner logs a pot award.
func (lv *LogView) AddWinner(playerName string, amount int64, handRank string) {
	if handRank != "" {
		lv.Add(LogKindWinner, fmt.Sprintf("%s wins $%d with %s", playerName, amount, handRank))
	} else {
		lv.Add(LogKindWinner, fmt.Sprintf("%s wins $%d", playerName, amount))
	}
}

// AddSystem logs a system event (deal, shuffle, etc.).
func (lv *LogView) AddSystem(text string) {
	lv.Add(LogKindSystem, text)
}

// AddError logs an error or invalid action.
func (lv *LogView) AddError(text string) {
	lv.Add(LogKindError, text)
}

// AddNetwork logs a network event.
func (lv *LogView) AddNetwork(text string) {
	lv.Add(LogKindNetwork, text)
}

// ScrollUp moves the viewport up by one line.
func (lv *LogView) ScrollUp() {
	if lv.ScrollTop > 0 {
		lv.ScrollTop--
	}
}

// ScrollDown moves the viewport down by one line.
func (lv *LogView) ScrollDown() {
	max := len(lv.Entries) - lv.MaxVisible
	if max < 0 {
		max = 0
	}
	if lv.ScrollTop < max {
		lv.ScrollTop++
	}
}

// Render renders the log panel as a multi-line string.
func (lv *LogView) Render() string {
	// Collect visible entries.
	end := lv.ScrollTop + lv.MaxVisible
	if end > len(lv.Entries) {
		end = len(lv.Entries)
	}
	visible := lv.Entries[lv.ScrollTop:end]

	lines := make([]string, lv.MaxVisible)
	for i := range lines {
		if i < len(visible) {
			lines[i] = renderLogEntry(visible[i])
		} else {
			lines[i] = "" // empty filler
		}
	}

	// Scroll indicator.
	scrollIndicator := ""
	if len(lv.Entries) > lv.MaxVisible {
		total := len(lv.Entries)
		pos := lv.ScrollTop + lv.MaxVisible
		if pos > total {
			pos = total
		}
		scrollIndicator = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#444444")).
			Render(fmt.Sprintf(" [%d/%d] ↑↓ scroll", pos, total))
	}

	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#666666")).
		Render("ACTION LOG") + scrollIndicator

	content := title + "\n" + strings.Join(lines, "\n")
	return StyleLogPanel.Render(content)
}

// renderLogEntry styles a single log entry by kind.
func renderLogEntry(e LogEntry) string {
	ts := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#444444")).
		Render(e.Timestamp.Format("15:04:05") + " ")

	var body string
	switch e.Kind {
	case LogKindAction:
		body = StyleLogEntryAction.Render(e.Text)
	case LogKindSystem:
		body = StyleLogEntryHighlight.Render(e.Text)
	case LogKindWinner:
		body = StyleLogEntryWinner.Render(e.Text)
	case LogKindError:
		body = StyleLogEntryError.Render(e.Text)
	case LogKindNetwork:
		body = lipgloss.NewStyle().Foreground(lipgloss.Color("#5090e0")).Render(e.Text)
	default:
		body = StyleLogEntry.Render(e.Text)
	}

	return ts + body
}
