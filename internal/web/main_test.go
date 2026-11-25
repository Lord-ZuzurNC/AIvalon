package web

import (
	"os"
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/i18n"
)

// TestMain loads the real web/static/i18n/*.json files before any test
// in this package runs — same registry a built binary would load at
// startup (see cmd/aivalon/main.go), just reached via a relative path
// from this package's own directory ("go test" runs with the package
// dir as cwd) instead of the repo root. Without this, every test in
// this package would only ever see i18n's tiny builtinFallback (still
// correct for English — see its own doc comment — but it can never
// prove a *second* language, like TestLanguageInstruction's "fr"
// assertion needs to).
func TestMain(m *testing.M) {
	reg, err := i18n.LoadDir("../../web/static/i18n")
	if err != nil {
		// Not fatal — i18n falls back to English-only — but a test
		// run silently losing French coverage should be loud.
		panic("web package tests: loading web/static/i18n: " + err.Error())
	}
	i18n.SetDefault(reg)
	os.Exit(m.Run())
}
