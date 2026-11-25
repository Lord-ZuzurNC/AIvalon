package langdetect

import "testing"

func TestDetect_ConfidentFrenchVsEnglish(t *testing.T) {
	fr := "Un sage sorcier. Il est calme et patient. Scénario : il enseigne la magie à son apprentie. Bonjour, jeune apprenti."
	res, ok := Detect(fr)
	if !ok {
		t.Fatalf("expected a confident detection for clearly French text")
	}
	if res.Code != "fr" {
		t.Fatalf("got code %q, want fr (name=%q)", res.Code, res.Name)
	}

	en := "A wise wizard. He is calm and patient. Scenario: he teaches magic to his apprentice. Hello, young apprentice."
	res, ok = Detect(en)
	if !ok {
		t.Fatalf("expected a confident detection for clearly English text")
	}
	if res.Code != "en" {
		t.Fatalf("got code %q, want en (name=%q)", res.Code, res.Name)
	}
}

func TestDetect_TooShortIsSkippedNotMisdetected(t *testing.T) {
	// A single short, otherwise-unambiguous English sentence can
	// still score a low, wrong-language confidence from whatlanggo
	// (observed: "Afrikaans" at 0.19 during development) — exactly
	// the false-positive handlers_import.go must never act on.
	if _, ok := Detect("A wise wizard living in a remote tower."); ok {
		t.Fatal("expected a short sentence to be reported as too ambiguous to act on")
	}
	if _, ok := Detect("Hello."); ok {
		t.Fatal("expected a tiny greeting to be reported as too ambiguous to act on")
	}
	if _, ok := Detect(""); ok {
		t.Fatal("expected empty text to be reported as too ambiguous to act on")
	}
	if _, ok := Detect("   \n\t  "); ok {
		t.Fatal("expected whitespace-only text to be reported as too ambiguous to act on")
	}
}

func TestDetect_NonLatinScript(t *testing.T) {
	res, ok := Detect("这是一个古老的巫师，住在一座孤独的塔中，教导他年轻的学徒魔法的奥秘，并守护着这片森林深处的秘密。")
	if !ok {
		t.Fatal("expected a confident detection for Chinese text")
	}
	if res.Code != "zh" {
		t.Fatalf("got code %q, want zh (name=%q)", res.Code, res.Name)
	}
	if res.Name == "" {
		t.Fatal("expected a non-empty fallback display name")
	}
}
