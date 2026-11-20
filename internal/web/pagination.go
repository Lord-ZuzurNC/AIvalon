package web

import (
	"fmt"
	"net/http"
	"strconv"
)

// Page sizes for the server-side, ?page=N-driven lists (Chat sessions,
// World lorebooks, Character cards, a lorebook's own entries). Grid
// cards are much taller than a list row, hence the smaller page.
const (
	gridPageSize = 24
	listPageSize = 50
)

// Pager is what the pager component (see templates.templ) needs to draw
// "← Prev · Page N of M · Next →" under a paginated list.
type Pager struct {
	BaseURL    string
	Page       int
	TotalPages int
	Size       int
	// Requested is true when the URL carried ?page= at all — lets a
	// collapsed-by-default section (LorebookPage's entries) stay open
	// while the user is actively paging through it.
	Requested bool
}

// Offset is the SQL OFFSET for this page.
func (p Pager) Offset() int { return (p.Page - 1) * p.Size }

func (p Pager) Href(page int) string { return fmt.Sprintf("%s?page=%d", p.BaseURL, page) }

// newPager reads ?page= (default 1, anything unparsable or < 1 is 1) and
// clamps it to the last page, so a stale link to page 9 after deleting
// half the items lands on the real last page instead of an empty one.
func newPager(r *http.Request, baseURL string, total, size int) Pager {
	pages := (total + size - 1) / size
	if pages < 1 {
		pages = 1
	}
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	return Pager{BaseURL: baseURL, Page: page, TotalPages: pages, Size: size, Requested: r.URL.Query().Has("page")}
}

// pageOf slices an already-fully-loaded list down to p's page — for
// lists that are loaded whole anyway (a lorebook's entries are, for
// the entry count and the chat-side resolver), so a LIMIT query would
// only add a second round-trip.
func pageOf[T any](items []T, p Pager) []T {
	start := min(p.Offset(), len(items))
	end := min(start+p.Size, len(items))
	return items[start:end]
}

// viewPageSize picks the page size for a page with a List/Grid toggle.
// The view choice lives client-side (viewToggleScript), which mirrors
// it into an "aivalon-view-<key>" cookie so the server can size the
// page to match: Grid (the default) gets gridPageSize, List gets
// listPageSize. Both panels render the same slice, so flipping the
// toggle mid-page shows the same items; the new size applies on the
// next navigation.
func viewPageSize(r *http.Request, viewKey string) int {
	if c, err := r.Cookie("aivalon-view-" + viewKey); err == nil && c.Value == "list" {
		return listPageSize
	}
	return gridPageSize
}
