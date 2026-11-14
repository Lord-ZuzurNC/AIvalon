package web

import (
	"html"
	"strings"
)

// formatMessageContent turns raw message text into safe HTML in two passes.
//
// Pass 1 splits the content on *asterisks* into action/plain segments —
// asterisk text becomes de-emphasized action/narration (asterisks
// stripped, they're just the delimiter).
//
// Pass 2 looks at each plain segment for quotes. Local models (observed:
// Gemma) default to wrapping an ENTIRE spoken line in one quote pair as a
// writing habit, not as meaningful emphasis — bolding that made nearly
// every reply end up all-bold, which is what Lord flagged. So: if a plain
// segment IS, in full, one quote-wrapped block ("..." and nothing outside
// it), the quote marks are just that default habit — they're stripped and
// the text renders plain. A quote that appears nested inside otherwise
// unquoted text (e.g. `He said "hello" and left.`) is the genuinely
// meaningful case — a character quoting someone else's words — and that
// one keeps its quote marks and gets bolded.
//
// Every character still passes through html.EscapeString before being
// written — this is LLM output rendered as HTML, escaping is not optional
// regardless of which span it lands in.
func formatMessageContent(content string) string {
	var out strings.Builder
	for _, seg := range splitActionSegments(content) {
		if seg.isAction {
			out.WriteString(`<span class="action">`)
			out.WriteString(escapeForHTML(seg.text))
			out.WriteString(`</span>`)
		} else {
			out.WriteString(formatProseSegment(seg.text))
		}
	}
	return out.String()
}

type contentSegment struct {
	text     string
	isAction bool
}

// splitActionSegments splits on *asterisks*, same graceful-degradation
// rule as before: an unbalanced trailing asterisk just runs to the end of
// the string as one action segment rather than erroring.
func splitActionSegments(content string) []contentSegment {
	var segs []contentSegment
	var seg strings.Builder
	inAction := false
	flush := func() {
		if seg.Len() > 0 {
			segs = append(segs, contentSegment{text: seg.String(), isAction: inAction})
			seg.Reset()
		}
	}
	for _, r := range content {
		if r == '*' {
			flush()
			inAction = !inAction
			continue
		}
		seg.WriteRune(r)
	}
	flush()
	return segs
}

const quoteChar = `"`

// formatProseSegment handles one non-action segment: strip a single outer
// quote-wrap if the whole (trimmed) segment is one quoted block, otherwise
// leave quotes as-is and let toggleQuoteFormat bold any nested ones.
func formatProseSegment(text string) string {
	leftTrimmed := strings.TrimLeft(text, " \t\r\n")
	leadWS := text[:len(text)-len(leftTrimmed)]
	trimmed := strings.TrimRight(leftTrimmed, " \t\r\n")
	trailWS := leftTrimmed[len(trimmed):]

	body := trimmed
	if len(trimmed) >= 2 && strings.HasPrefix(trimmed, quoteChar) && strings.HasSuffix(trimmed, quoteChar) {
		body = trimmed[1 : len(trimmed)-1]
	}
	return escapeForHTML(leadWS) + toggleQuoteFormat(body) + escapeForHTML(trailWS)
}

// toggleQuoteFormat bolds genuinely nested "quoted" spans within text that
// isn't itself a single full quote-wrap (that case is handled, and its
// quotes already stripped, by formatProseSegment before this runs).
func toggleQuoteFormat(text string) string {
	var out, seg strings.Builder
	inQuote := false
	flush := func() {
		if seg.Len() == 0 {
			return
		}
		escaped := escapeForHTML(seg.String())
		if inQuote {
			out.WriteString(`<strong class="dialogue">`)
			out.WriteString(escaped)
			out.WriteString(`</strong>`)
		} else {
			out.WriteString(escaped)
		}
		seg.Reset()
	}
	for _, r := range text {
		if r == '"' {
			if inQuote {
				seg.WriteRune(r)
				flush()
				inQuote = false
			} else {
				flush()
				inQuote = true
				seg.WriteRune(r)
			}
			continue
		}
		seg.WriteRune(r)
	}
	flush()
	return out.String()
}

func escapeForHTML(s string) string {
	return strings.ReplaceAll(html.EscapeString(s), "\n", "<br>")
}
