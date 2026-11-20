package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// AppSettings holds small global settings not tied to any one backend
// profile or session. Backed by the single row in app_settings (id=1,
// seeded by migration 00002) — there's exactly one of these per install.
//
// UpdateAppSettings writes every field, so a handler changing one of
// them must GetAppSettings first and modify that, not build a fresh
// struct — otherwise the other fields get zeroed.
type AppSettings struct {
	SystemPromptTemplate string
	// PNGImportWhitelist: hostnames (lowercase, no port) the "From URL"
	// import may fetch Character Card PNGs from, beyond loopback. Stored
	// one per line (migration 00010).
	PNGImportWhitelist []string
	// ContextBudgetChars caps system prompt + history sent per reply, in
	// characters (~4 chars/token is the rough rule — a character count,
	// not a real token count, since tokenizers differ per backend/model).
	// 0 means no limit. Migration 00011, default 24000.
	ContextBudgetChars int
}

// DefaultSystemPromptTemplate mirrors the seed/migration-updated value in
// internal/db/migrations (00002 then 00003), so "reset to default" in
// Settings doesn't need its own round-trip to re-read it.
const DefaultSystemPromptTemplate = `You are a skilled, creative author collaborating on an immersive interactive story with {{persona_name}}. Your role is to narrate the world and give voice to its characters, advancing the story in ways that are engaging, coherent, and true to the established tone. You are the narrator — not any single character in it.

Guidelines:
- Write in third-person limited perspective unless a character card specifies otherwise.
- Stay fully in the narrative. Do not break the fourth wall, add disclaimers, or remind {{persona_name}} this is fiction.
- Match the emotional register and pacing of {{persona_name}}'s messages. If they write short, punchy replies, mirror that energy.
- End responses at a natural story beat. Do not end with a question unless it fits the scene organically.
- Prioritize character consistency above all else. A character's voice, mannerisms, and worldview should remain stable across the entire session.

{{lorebook}}

{{characters}}

Formatting rules:
- "Quotes" are for anything a character actually says out loud, and for their internal thoughts.
- *Asterisks* are for actions, gestures, and narration beats.
- When a character speaks, write their name followed by a colon before their line, like Name: "..." — this disambiguates who's talking when more than one character is present.
- Never write {{persona_name}}'s actions, words, or decisions — only react to what they actually said.

{{persona_name}} is the player, speaking as themselves: {{persona_description}}

If {{persona_name}} asks one character to relay something to another and report back, narrate the whole exchange, including the other character's actual reply, not just the first character's side of it.`

func GetAppSettings(db *sql.DB) (AppSettings, error) {
	var s AppSettings
	var whitelist string
	err := db.QueryRow(`SELECT system_prompt_template, png_import_whitelist, context_budget_chars FROM app_settings WHERE id = 1`).
		Scan(&s.SystemPromptTemplate, &whitelist, &s.ContextBudgetChars)
	if err != nil {
		return AppSettings{}, fmt.Errorf("getting app settings: %w", err)
	}
	s.PNGImportWhitelist = SplitHostList(whitelist)
	return s, nil
}

func UpdateAppSettings(db *sql.DB, s AppSettings) error {
	_, err := db.Exec(`UPDATE app_settings SET system_prompt_template = ?, png_import_whitelist = ?, context_budget_chars = ? WHERE id = 1`,
		s.SystemPromptTemplate, JoinHostList(s.PNGImportWhitelist), s.ContextBudgetChars)
	if err != nil {
		return fmt.Errorf("updating app settings: %w", err)
	}
	return nil
}

// SplitHostList/JoinHostList convert between the whitelist's stored /
// textarea form (one hostname per line) and the []string the rest of
// the app uses — same shape as web's splitKeys/joinKeys for lorebook
// keys, but newline-separated since that's how a textarea reads.
// Commas and spaces are accepted as separators too (a pasted
// "a.com, b.com" shouldn't become one bogus host), entries are
// lowercased, and blanks/duplicates dropped.
func SplitHostList(s string) []string {
	var hosts []string
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' || r == ' ' || r == '\t' }) {
		h := strings.ToLower(strings.TrimSpace(part))
		if h != "" && !seen[h] {
			seen[h] = true
			hosts = append(hosts, h)
		}
	}
	return hosts
}

func JoinHostList(hosts []string) string {
	return strings.Join(hosts, "\n")
}
