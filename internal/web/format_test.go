package web

import (
	"strings"
	"testing"
)

func TestStripSpeakerEcho(t *testing.T) {
	cases := []struct{ name, content, speaker, want string }{
		{"plain echo", "Merlin: *He closes the book.*", "Merlin", "*He closes the book.*"},
		{"scene-break noise before the echo", "\n\n***\n\nGorak: *He shifts his weight.*", "Gorak", "*He shifts his weight.*"},
		{"case-insensitive", "merlin: hello", "Merlin", "hello"},
		{"no echo, left untouched", "*He waves.* \"Hello.\"", "Merlin", "*He waves.* \"Hello.\""},
		{"different name isn't stripped", "Gorak: hi", "Merlin", "Gorak: hi"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stripSpeakerEcho(c.content, c.speaker); got != c.want {
				t.Errorf("stripSpeakerEcho(%q, %q) = %q, want %q", c.content, c.speaker, got, c.want)
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
