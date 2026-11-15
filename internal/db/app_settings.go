package db

import (
	"database/sql"
	"fmt"
)

// AppSettings holds small global settings not tied to any one backend
// profile or session. Backed by the single row in app_settings (id=1,
// seeded by migration 00002) — there's exactly one of these per install.
type AppSettings struct {
	SystemPromptTemplate string
}

// DefaultSystemPromptTemplate mirrors the seed in migration
// 00002_app_settings.sql, so "reset to default" in Settings doesn't need
// its own round-trip to re-read the migration's seed value.
const DefaultSystemPromptTemplate = `You are the narrator of this world — not any single character in it. Think of the world as its lorebook describes it.

{{lorebook}}

{{characters}}

{{persona_name}} is the player, speaking as themselves: {{persona_description}}
Never write {{persona_name}}'s actions, words, or decisions — only react to what they actually said.

How to write your replies:
- Narrate the scene and whichever characters' actions or dialogue the moment calls for. You are not limited to one character.
- Use "quotes" for anything a character actually says out loud.
- Use *asterisks* for actions, gestures, and narration beats.
- When a character speaks, write their name followed by a colon before their line, like Name: "..." — this makes it clear who's talking when more than one character is present.
- If the player asks one character to relay something to another and report back, narrate the whole exchange, including the other character's actual reply.`

func GetAppSettings(db *sql.DB) (AppSettings, error) {
	var s AppSettings
	err := db.QueryRow(`SELECT system_prompt_template FROM app_settings WHERE id = 1`).Scan(&s.SystemPromptTemplate)
	if err != nil {
		return AppSettings{}, fmt.Errorf("getting app settings: %w", err)
	}
	return s, nil
}

func UpdateAppSettings(db *sql.DB, s AppSettings) error {
	_, err := db.Exec(`UPDATE app_settings SET system_prompt_template = ? WHERE id = 1`, s.SystemPromptTemplate)
	if err != nil {
		return fmt.Errorf("updating app settings: %w", err)
	}
	return nil
}
