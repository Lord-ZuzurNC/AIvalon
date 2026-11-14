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
			"quote kept and bolded (escaped, as it must be)",
			`"hello there"`,
			`<strong class="dialogue">&#34;hello there&#34;</strong>`,
		},
		{
			"mixed, matches Lord's actual roleplay style",
			`*leans back* "I am Merlin." *smiles*`,
			`<span class="action">leans back</span> <strong class="dialogue">&#34;I am Merlin.&#34;</strong> <span class="action">smiles</span>`,
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
