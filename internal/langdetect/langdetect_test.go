package langdetect

import (
	"os"
	"testing"

	"github.com/abadojack/whatlanggo"
)

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

// TestDetect_RealCardFailsPriorToLengthCap documents the exact
// pathology this package's maxDetectLength exists to dodge: fed
// whatlanggo.Detect directly (bypassing our own truncation), the real
// card text that triggered Lord's bug report scores a deeply NEGATIVE
// confidence for English — its own correct top guess — because the
// text is long enough that the runner-up language's trigram distance
// overflows whatlanggo's internal "no match" ceiling (see
// docs/DESIGN.md's "Import language detection" section). This test
// exists to prove the failure mode is real, not hypothetical; it does
// not call our own Detect (which already guards against it).
func TestDetect_RealCardFailsPriorToLengthCap(t *testing.T) {
	raw, err := os.ReadFile("testdata/veyra_card_text.txt")
	if err != nil {
		t.Fatalf("reading testdata: %v", err)
	}
	info := whatlanggo.Detect(string(raw))
	if info.Lang.Iso6391() != "en" {
		t.Fatalf("expected whatlanggo's own top guess to still be English, got %q — this test's premise may be stale", info.Lang.Iso6391())
	}
	if info.Confidence >= 0 {
		t.Fatalf("expected the raw, untruncated card text to reproduce whatlanggo's negative-confidence pathology (got %.3f) — if this starts failing, whatlanggo's algorithm or its data changed and maxDetectLength's rationale in docs/DESIGN.md needs re-checking", info.Confidence)
	}
}

// TestDetect_RealCardIsDetectedAfterLengthCap is the actual
// regression test for the bug Lord hit: the same real card text,
// through THIS package's own Detect (which truncates to
// maxDetectLength before calling whatlanggo), must now come back
// ok=true with code "en" — before the maxDetectLength fix, this
// assertion failed (Detect returned ok=false, exactly like Lord saw:
// no warning, no VO/VF, silent import of an English card into a
// French-targeted install).
func TestDetect_RealCardIsDetectedAfterLengthCap(t *testing.T) {
	raw, err := os.ReadFile("testdata/veyra_card_text.txt")
	if err != nil {
		t.Fatalf("reading testdata: %v", err)
	}
	res, ok := Detect(string(raw))
	if !ok {
		t.Fatal("expected the real card's full text to be confidently detected as English after the maxDetectLength fix — this is the exact regression Lord hit")
	}
	if res.Code != "en" {
		t.Fatalf("got code %q, want en (name=%q)", res.Code, res.Name)
	}
}
