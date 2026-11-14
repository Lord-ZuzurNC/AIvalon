package web

import (
	"html"
	"strings"
)

// formatMessageContent turns raw message text into safe HTML: text wrapped
// in *asterisks* becomes de-emphasized action/narration (asterisks
// stripped, they're just the delimiter), text inside "quotes" becomes
// emphasized dialogue (the quote marks themselves are kept — they're part
// of how dialogue reads, not a delimiter to discard).
//
// This is a flat, non-nesting state machine: it does not handle a quote
// inside an action or vice versa specially, which roleplay text essentially
// never needs. Every character still passes through html.EscapeString
// before being written — this is LLM output rendered as HTML, escaping is
// not optional regardless of which span it lands in.
func formatMessageContent(content string) string {
	var out, seg strings.Builder
	const (
		plain  = 0
		action = 1
		quote  = 2
	)
	state := plain

	flush := func() {
		if seg.Len() == 0 {
			return
		}
		text := strings.ReplaceAll(html.EscapeString(seg.String()), "\n", "<br>")
		switch state {
		case action:
			out.WriteString(`<span class="action">`)
			out.WriteString(text)
			out.WriteString(`</span>`)
		case quote:
			out.WriteString(`<strong class="dialogue">`)
			out.WriteString(text)
			out.WriteString(`</strong>`)
		default:
			out.WriteString(text)
		}
		seg.Reset()
	}

	for _, r := range content {
		switch r {
		case '*':
			flush()
			if state == action {
				state = plain
			} else {
				state = action
			}
		case '"':
			if state == quote {
				seg.WriteRune(r)
				flush()
				state = plain
			} else {
				flush()
				state = quote
				seg.WriteRune(r)
			}
		default:
			seg.WriteRune(r)
		}
	}
	flush()
	return out.String()
}
