package db

import (
	"path/filepath"
	"testing"
)

func TestVaultedCardsAndLorebooksHiddenFromBrowsingOnly(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	profile, _ := CreateBackendProfile(sqlDB, BackendProfile{Name: "p", Kind: "llm", BaseURL: "http://x"})
	shown, _ := CreateCharacterCard(sqlDB, CharacterCard{Name: "Arthur"})
	hidden, _ := CreateCharacterCard(sqlDB, CharacterCard{Name: "Merlin"})
	shownBook, _ := CreateLorebook(sqlDB, Lorebook{Name: "Camelot"})
	hiddenBook, _ := CreateLorebook(sqlDB, Lorebook{Name: "Avalon"})
	session, err := CreateSession(sqlDB, Session{Title: "Q", BackendProfileID: profile, LorebookID: hiddenBook})
	if err != nil {
		t.Fatal(err)
	}
	if err := AttachCharacterToSession(sqlDB, session, hidden, 0); err != nil {
		t.Fatal(err)
	}
	if err := SetCharacterCardVaulted(sqlDB, hidden, true); err != nil {
		t.Fatal(err)
	}
	if err := SetLorebookVaulted(sqlDB, hiddenBook, true); err != nil {
		t.Fatal(err)
	}

	cards, _ := ListCharacterCards(sqlDB, -1, 0)
	if len(cards) != 1 || cards[0].ID != shown {
		t.Fatalf("ListCharacterCards = %+v, want only Arthur", cards)
	}
	if n, _ := CountCharacterCards(sqlDB); n != 1 {
		t.Fatalf("CountCharacterCards = %d, want 1", n)
	}
	vc, _ := ListVaultedCharacterCards(sqlDB)
	if len(vc) != 1 || vc[0].ID != hidden || !vc[0].IsVaulted {
		t.Fatalf("ListVaultedCharacterCards = %+v, want only Merlin", vc)
	}
	books, _ := ListLorebooks(sqlDB, -1, 0)
	if len(books) != 1 || books[0].ID != shownBook {
		t.Fatalf("ListLorebooks = %+v, want only Camelot", books)
	}
	if n, _ := CountLorebooks(sqlDB); n != 1 {
		t.Fatalf("CountLorebooks = %d, want 1", n)
	}
	vb, _ := ListVaultedLorebooks(sqlDB)
	if len(vb) != 1 || vb[0].ID != hiddenBook {
		t.Fatalf("ListVaultedLorebooks = %+v, want only Avalon", vb)
	}

	// The running chat must keep its vaulted character and lorebook.
	attached, err := ListCharacterCardsForSession(sqlDB, session)
	if err != nil || len(attached) != 1 || attached[0].ID != hidden || attached[0].Name != "Merlin" {
		t.Fatalf("ListCharacterCardsForSession = %+v, %v; want Merlin still attached", attached, err)
	}
	if c, err := GetCharacterCard(sqlDB, hidden); err != nil || !c.IsVaulted {
		t.Fatalf("GetCharacterCard = %+v, %v; want vaulted card still resolvable", c, err)
	}
	if b, err := GetLorebook(sqlDB, hiddenBook); err != nil || !b.IsVaulted {
		t.Fatalf("GetLorebook = %+v, %v; want vaulted book still resolvable", b, err)
	}

	if err := SetCharacterCardVaulted(sqlDB, hidden, false); err != nil {
		t.Fatal(err)
	}
	if cards, _ := ListCharacterCards(sqlDB, -1, 0); len(cards) != 2 {
		t.Fatalf("after un-vaulting, ListCharacterCards has %d cards, want 2", len(cards))
	}
}
