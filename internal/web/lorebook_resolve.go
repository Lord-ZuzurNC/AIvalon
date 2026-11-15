package web

import (
	"math/rand"
	"regexp"
	"sort"
	"strings"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

// resolveLorebookContent decides which of a lorebook's entries are active
// for this turn and joins their content in insertion order. "constant"
// entries are always active; "selective" entries need at least one of
// their Keys to appear as a whole word in lastUserMessage (case-
// insensitive) — same word-boundary reasoning addressedCharacters used in
// an earlier build of this app: a plain substring check would let a
// short key false-positive inside an unrelated longer word (e.g. key
// "Mer" matching inside "Merlinette"). Probability further dampens an
// already-triggered entry: 100 (the default) always passes without
// calling roll at all, which is what keeps the boundary cases
// deterministic and testable; roll is only ever consulted below 100.
func resolveLorebookContent(entries []db.LorebookEntry, lastUserMessage string, roll func(probability int) bool) string {
	sorted := make([]db.LorebookEntry, len(entries))
	copy(sorted, entries)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].InsertionOrder < sorted[j].InsertionOrder })

	var parts []string
	for _, e := range sorted {
		if !e.Enabled {
			continue
		}
		triggered := e.TriggerStrategy == "constant" || keysMatch(e.Keys, lastUserMessage)
		if !triggered {
			continue
		}
		if e.Probability < 100 && !roll(e.Probability) {
			continue
		}
		parts = append(parts, e.Content)
	}
	return strings.Join(parts, "\n\n")
}

func keysMatch(keys []string, message string) bool {
	for _, k := range keys {
		if k == "" {
			continue
		}
		pattern := `(?i)\b` + regexp.QuoteMeta(k) + `\b`
		if matched, err := regexp.MatchString(pattern, message); err == nil && matched {
			return true
		}
	}
	return false
}

func defaultProbabilityRoll(probability int) bool {
	return rand.Intn(100) < probability
}

func lastUserMessageContent(history []db.ChatMessage) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			return history[i].Content
		}
	}
	return ""
}
