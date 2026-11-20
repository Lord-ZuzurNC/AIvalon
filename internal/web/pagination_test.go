package web

import (
	"net/http/httptest"
	"testing"
)

func TestNewPagerClampsAndSlices(t *testing.T) {
	cases := []struct {
		query         string
		total, want   int
		wantRequested bool
	}{
		{"", 120, 1, false},
		{"?page=0", 120, 1, true},
		{"?page=abc", 120, 1, true},
		{"?page=2", 120, 2, true},
		{"?page=99", 120, 3, true}, // clamped to the last page (120/50 → 3)
		{"?page=5", 0, 1, true},    // empty list still has one (empty) page
	}
	for _, c := range cases {
		p := newPager(httptest.NewRequest("GET", "/x"+c.query, nil), "/x", c.total, 50)
		if p.Page != c.want || p.Requested != c.wantRequested {
			t.Errorf("%q total=%d: got page %d requested %v, want %d %v", c.query, c.total, p.Page, p.Requested, c.want, c.wantRequested)
		}
	}

	items := make([]int, 120)
	for i := range items {
		items[i] = i
	}
	p := newPager(httptest.NewRequest("GET", "/x?page=3", nil), "/x", len(items), 50)
	got := pageOf(items, p)
	if len(got) != 20 || got[0] != 100 {
		t.Fatalf("last page slice wrong: len %d first %v", len(got), got)
	}
	if p.Href(2) != "/x?page=2" {
		t.Fatalf("Href: %q", p.Href(2))
	}
}
