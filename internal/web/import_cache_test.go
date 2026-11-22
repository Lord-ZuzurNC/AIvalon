package web

import "testing"

func TestImportCache_StashAndTake(t *testing.T) {
	c := NewImportCache()
	token, err := c.Stash([]byte(`{"hello":"world"}`), "test.json", true)
	if err != nil {
		t.Fatalf("Stash: %v", err)
	}
	raw, filename, vaulted, ok := c.Take(token)
	if !ok {
		t.Fatal("expected Take to find the stashed upload")
	}
	if string(raw) != `{"hello":"world"}` || filename != "test.json" || !vaulted {
		t.Fatalf("got raw=%q filename=%q vaulted=%v", raw, filename, vaulted)
	}
	// A token is redeemed at most once.
	if _, _, _, ok := c.Take(token); ok {
		t.Fatal("expected a second Take of the same token to fail")
	}
}

func TestImportCache_UnknownToken(t *testing.T) {
	c := NewImportCache()
	if _, _, _, ok := c.Take("not-a-real-token"); ok {
		t.Fatal("expected an unknown token to fail")
	}
}
