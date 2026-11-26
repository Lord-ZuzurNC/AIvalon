package web

import "net/http"

// AppVersion is bumped by hand at notable milestones — no CI/release
// tooling yet for a solo local app, see AboutPage.
const AppVersion = "v0.2-beta-1"

// RepoURL and License back AboutPage's disclosure — kept as named
// constants (not inlined in the templ) so they're easy to find if
// either ever changes.
const (
	RepoURL = "https://github.com/Lord-ZuzurNC/AIvalon"
	License = "GNU Affero General Public License v3.0 (AGPL-3.0)"
)

// About serves the "About" page linked from the sidebar footer — pure
// static content, no DB access needed.
func About(w http.ResponseWriter, r *http.Request) {
	render(w, r, AboutPage())
}
