package web

import "testing"

func TestTruncate(t *testing.T) {
	cases := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"shorter than limit", "hello", 10, "hello"},
		{"exactly at limit", "hello", 5, "hello"},
		{"cut with ellipsis", "hello world", 5, "hello…"},
		{"trims trailing space before ellipsis", "hello world", 6, "hello…"},
		{"empty string", "", 5, ""},
		{"multi-byte runes not split mid-sequence", "héllo world", 2, "hé…"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := truncate(c.s, c.n); got != c.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
			}
		})
	}
}
