package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

// mockA1111 answers txt2img with imageB64 and seed 4242 in info.
func mockA1111(t *testing.T, imageB64 string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info, _ := json.Marshal(map[string]any{"seed": 4242})
		json.NewEncoder(w).Encode(map[string]any{"images": []string{imageB64}, "info": string(info)})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func imageForm(profileID int64, extra url.Values) url.Values {
	v := url.Values{
		"backend_profile_id": {fmt.Sprint(profileID)},
		"prompt":             {"a castle on a hill"},
		"style_suffix":       {"watercolor"},
		"negative_prompt":    {"blurry"},
		"steps":              {"20"},
		"cfg_scale":          {"7"},
		"width":              {"512"},
		"height":             {"512"},
		"sampler_name":       {"Euler a"},
		"seed":               {"-1"},
	}
	for k, vs := range extra {
		v[k] = vs
	}
	return v
}

func postForm(h http.HandlerFunc, sessionID int64, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/x", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	r.SetPathValue("id", fmt.Sprint(sessionID))
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestGenerateImageEndToEnd(t *testing.T) {
	f := newFixture(t)
	t.Chdir(t.TempDir()) // uploads land under ./web/static/uploads
	srv := mockA1111(t, tinyPNGBase64)
	profileID, err := db.CreateBackendProfile(f.db, db.BackendProfile{Name: "sd", Kind: "image", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	h := ChatHandlers{DB: f.db}

	w := postForm(h.GenerateImage, f.session, imageForm(profileID, nil))
	if got := w.Header().Get("HX-Redirect"); got != fmt.Sprintf("/chat/%d", f.session) {
		t.Fatalf("HX-Redirect = %q, body: %s", got, w.Body.String())
	}

	messages, err := db.ListMessages(f.db, f.session)
	if err != nil {
		t.Fatal(err)
	}
	img := messages[len(messages)-1]
	if img.Role != "image" || img.ImagePath == "" {
		t.Fatalf("last message = %+v, want an image message with image_path", img)
	}
	if !strings.HasPrefix(img.ImagePath, fmt.Sprintf("uploads/generated/%d.png?v=", img.ID)) {
		t.Fatalf("image_path = %q, want saveImageBytes' uploads/generated/<msgID>.png?v=…", img.ImagePath)
	}
	if !strings.Contains(img.Content, "a castle on a hill, watercolor") || !strings.Contains(img.Content, "seed 4242") {
		t.Fatalf("content = %q, want prompt+suffix and the backend's seed", img.Content)
	}
	onDisk, err := os.ReadFile(filepath.Join("web", "static", "uploads", "generated", fmt.Sprintf("%d.png", img.ID)))
	if err != nil {
		t.Fatalf("generated file not on disk: %v", err)
	}
	want, _ := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if !bytes.Equal(onDisk, want) {
		t.Fatal("file on disk differs from the backend's image")
	}

	// The next ordinary message lands after the image.
	postForm(h.PostMessage, f.session, url.Values{"content": {"what a view"}})
	messages, _ = db.ListMessages(f.db, f.session)
	n := len(messages)
	if messages[n-2].ID != img.ID || messages[n-1].Role != "user" || messages[n-1].Content != "what a view" {
		t.Fatalf("order wrong — want …, image, user; got %+v", messages[n-2:])
	}

	// The page renders it inline and in the Gallery tab.
	r := httptest.NewRequest("GET", "/x", nil)
	r.SetPathValue("id", fmt.Sprint(f.session))
	page := httptest.NewRecorder()
	h.Show(page, r)
	src := `src="/static/` + img.ImagePath
	if c := strings.Count(page.Body.String(), src); c != 2 {
		t.Fatalf("image src rendered %d times, want 2 (message + gallery)", c)
	}
	if !strings.Contains(page.Body.String(), " download ") && !strings.Contains(page.Body.String(), " download>") {
		t.Fatal("gallery has no download link")
	}

	// And it never reaches the LLM prompt.
	chatMessages, _ := buildNarratorMessages("sys", db.DefaultPersona, nil, "", messages, 0)
	for _, m := range chatMessages {
		if strings.Contains(m.Content, "a castle on a hill") {
			t.Fatalf("image message leaked into the LLM history: %+v", m)
		}
	}
}

func TestGenerateImageRejectsNonImageBytes(t *testing.T) {
	f := newFixture(t)
	t.Chdir(t.TempDir())
	srv := mockA1111(t, base64.StdEncoding.EncodeToString([]byte("<html>definitely not a png</html>")))
	profileID, _ := db.CreateBackendProfile(f.db, db.BackendProfile{Name: "sd", Kind: "image", BaseURL: srv.URL})
	before := f.count(t, `SELECT COUNT(*) FROM messages WHERE session_id = ?`, f.session)

	w := postForm(ChatHandlers{DB: f.db}.GenerateImage, f.session, imageForm(profileID, nil))
	if w.Header().Get("HX-Redirect") != "" || !strings.Contains(w.Body.String(), "unsupported image type") {
		t.Fatalf("want writeImageToDisk's sniffing rejection; got redirect %q body %s", w.Header().Get("HX-Redirect"), w.Body.String())
	}
	if after := f.count(t, `SELECT COUNT(*) FROM messages WHERE session_id = ?`, f.session); after != before {
		t.Fatalf("message row left behind after failed save: %d → %d", before, after)
	}
	if entries, _ := os.ReadDir(filepath.Join("web", "static", "uploads", "generated")); len(entries) != 0 {
		t.Fatalf("files written despite rejection: %v", entries)
	}
}

func TestGenerateImageRefusesNonImageProfile(t *testing.T) {
	f := newFixture(t)
	llm, _ := db.CreateBackendProfile(f.db, db.BackendProfile{Name: "chat", Kind: "llm", BaseURL: "http://127.0.0.1:1"})
	w := postForm(ChatHandlers{DB: f.db}.GenerateImage, f.session, imageForm(llm, nil))
	if !strings.Contains(w.Body.String(), "pick an image backend profile") {
		t.Fatalf("body: %s", w.Body.String())
	}
}

func TestSaveImagePresetNeverSilentlyClobbers(t *testing.T) {
	f := newFixture(t)
	h := ChatHandlers{DB: f.db}
	steps := func(name string) (int, int) {
		presets, err := db.ListImagePresets(f.db)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range presets {
			if strings.EqualFold(p.Name, name) {
				return p.Steps, len(presets)
			}
		}
		return -1, len(presets)
	}
	origSteps, total := steps("Cyberpunk")

	// Taken name (different case), no overwrite: refused, untouched.
	w := postForm(h.SaveImagePreset, 0, imageForm(0, url.Values{"preset_name": {"cyberpunk"}, "steps": {"99"}}))
	if !strings.Contains(w.Body.String(), "already exists") {
		t.Fatalf("body: %s", w.Body.String())
	}
	if s, n := steps("Cyberpunk"); s != origSteps || n != total {
		t.Fatalf("refused save changed things: steps %d→%d, count %d→%d", origSteps, s, total, n)
	}

	// Explicit overwrite: updated in place, no new row, and the
	// refreshed <select> comes back out-of-band.
	w = postForm(h.SaveImagePreset, 0, imageForm(0, url.Values{"preset_name": {"cyberpunk"}, "steps": {"99"}, "overwrite": {"1"}}))
	if !strings.Contains(w.Body.String(), "Saved preset") || !strings.Contains(w.Body.String(), `hx-swap-oob="true"`) {
		t.Fatalf("body: %s", w.Body.String())
	}
	if s, n := steps("Cyberpunk"); s != 99 || n != total {
		t.Fatalf("overwrite: steps=%d count=%d, want 99/%d", s, n, total)
	}

	// New name: created from the submitted (current) values.
	postForm(h.SaveImagePreset, 0, imageForm(0, url.Values{"preset_name": {"Mine"}, "steps": {"33"}}))
	if s, n := steps("Mine"); s != 33 || n != total+1 {
		t.Fatalf("create: steps=%d count=%d", s, n)
	}

	// Delete.
	presets, _ := db.ListImagePresets(f.db)
	var mineID int64
	for _, p := range presets {
		if p.Name == "Mine" {
			mineID = p.ID
		}
	}
	postForm(h.DeleteImagePreset, 0, url.Values{"preset_id": {fmt.Sprint(mineID)}})
	if s, n := steps("Mine"); s != -1 || n != total {
		t.Fatalf("delete: still there (steps=%d count=%d)", s, n)
	}
}
