package web

import (
	"strings"
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

func TestStripSpeakerEcho(t *testing.T) {
	characters := []db.CharacterCard{{Name: "Merlin"}, {Name: "Merlinette"}, {Name: "Gorak"}}
	cases := []struct{ name, content, want string }{
		{"plain self-echo", "Merlin: *He closes the book.*", "*He closes the book.*"},
		{"scene-break noise before the echo", "\n\n***\n\nGorak: *He shifts his weight.*", "*He shifts his weight.*"},
		{"case-insensitive", "merlin: hello", "hello"},
		{"no echo, left untouched", "*He waves.* \"Hello.\"", "*He waves.* \"Hello.\""},
		{"name not in the session isn't stripped", "Someone: hi", "Someone: hi"},
		{
			"cross-character echo: model voiced a DIFFERENT attached character, strip that label too",
			`Merlinette: *She steps forward.*`,
			`*She steps forward.*`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stripSpeakerEcho(c.content, characters); got != c.want {
				t.Errorf("stripSpeakerEcho(%q, ...) = %q, want %q", c.content, got, c.want)
			}
		})
	}
}

func TestFormatMessageContent(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			"plain text",
			"hello world",
			"hello world",
		},
		{
			"action stripped of asterisks and greyed",
			"*waves*",
			`<span class="action">waves</span>`,
		},
		{
			"a message that's ENTIRELY one quoted block (the default model habit) loses its quotes and its bold",
			`"hello there"`,
			`hello there`,
		},
		{
			"same default habit, matches Lord's long-paragraph screenshot: whole line quoted, no bold",
			`"A wise strategy! I shall stand as the shield."`,
			`A wise strategy! I shall stand as the shield.`,
		},
		{
			"a genuinely nested quote inside otherwise unquoted narration stays bold",
			`He said "hello" and walked away.`,
			`He said <strong class="dialogue">&#34;hello&#34;</strong> and walked away.`,
		},
		{
			"mixed action + fully-quoted dialogue: action greyed, dialogue unquoted/unbolded",
			`*leans back* "I am Merlin." *smiles*`,
			`<span class="action">leans back</span> I am Merlin. <span class="action">smiles</span>`,
		},
		{
			"escapes HTML and never lets a raw tag through",
			`*<script>alert(1)</script>*`,
			`<span class="action">&lt;script&gt;alert(1)&lt;/script&gt;</span>`,
		},
		{
			"unbalanced asterisk degrades gracefully to end of string",
			"*trailing action with no closer",
			`<span class="action">trailing action with no closer</span>`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatMessageContent(c.in)
			if got != c.want {
				t.Errorf("formatMessageContent(%q)\n got:  %s\n want: %s", c.in, got, c.want)
			}
			if strings.Contains(got, "<script>") {
				t.Fatal("raw <script> leaked through unescaped")
			}
		})
	}
}
