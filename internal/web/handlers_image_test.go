package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
	"github.com/Lord-ZuzurNC/AIvalon/internal/imageclient"
	"github.com/Lord-ZuzurNC/AIvalon/internal/llmclient"
)

const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

// mockA1111 answers txt2img with imageB64 and seed 4242 in info; the
// returned func reports the body of the last txt2img request it
// received. Also answers unload-checkpoint (GenerateImage calls it
// right after a successful generation, see UnloadCheckpoint) with a
// bare 200 so it doesn't get mistaken for — or overwrite — the
// txt2img body callers are asserting against.
func mockA1111(t *testing.T, imageB64 string) (*httptest.Server, func() imageclient.GenerationParams) {
	t.Helper()
	var (
		mu   sync.Mutex
		last imageclient.GenerationParams
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sdapi/v1/unload-checkpoint" {
			json.NewEncoder(w).Encode(map[string]any{})
			return
		}
		var got imageclient.GenerationParams
		json.NewDecoder(r.Body).Decode(&got)
		mu.Lock()
		last = got
		mu.Unlock()
		info, _ := json.Marshal(map[string]any{"seed": 4242})
		json.NewEncoder(w).Encode(map[string]any{"images": []string{imageB64}, "info": string(info)})
	}))
	t.Cleanup(srv.Close)
	return srv, func() imageclient.GenerationParams {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
}

// imageForm is everything the Generate dialog still posts.
func imageForm(profileID int64) url.Values {
	return url.Values{"backend_profile_id": {fmt.Sprint(profileID)}, "prompt": {"a castle on a hill"}}
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
	srv, _ := mockA1111(t, tinyPNGBase64)
	profileID, err := db.CreateBackendProfile(f.db, db.BackendProfile{Name: "sd", Kind: "image", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	h := ChatHandlers{DB: f.db}

	w := postForm(h.GenerateImage, f.session, imageForm(profileID))
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
	if !strings.HasPrefix(img.Content, "a castle on a hill\n") || !strings.Contains(img.Content, "seed 4242") {
		t.Fatalf("content = %q, want the bare prompt (no preset → no suffix) and the backend's seed", img.Content)
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
	chatMessages, _, _ := buildNarratorMessages("sys", db.DefaultPersona, nil, "", messages, 0)
	for _, m := range chatMessages {
		if strings.Contains(m.Content, "a castle on a hill") {
			t.Fatalf("image message leaked into the LLM history: %+v", m)
		}
	}
}

func TestGenerateImageRejectsNonImageBytes(t *testing.T) {
	f := newFixture(t)
	t.Chdir(t.TempDir())
	srv, _ := mockA1111(t, base64.StdEncoding.EncodeToString([]byte("<html>definitely not a png</html>")))
	profileID, _ := db.CreateBackendProfile(f.db, db.BackendProfile{Name: "sd", Kind: "image", BaseURL: srv.URL})
	before := f.count(t, `SELECT COUNT(*) FROM messages WHERE session_id = ?`, f.session)

	w := postForm(ChatHandlers{DB: f.db}.GenerateImage, f.session, imageForm(profileID))
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
	w := postForm(ChatHandlers{DB: f.db}.GenerateImage, f.session, imageForm(llm))
	if !strings.Contains(w.Body.String(), "pick an image backend profile") {
		t.Fatalf("body: %s", w.Body.String())
	}
}

// presetByName finds a preset (case-insensitively) or returns ok=false.
func presetByName(t *testing.T, f fixture, name string) (db.ImagePreset, bool) {
	t.Helper()
	presets, err := db.ListImagePresets(f.db)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range presets {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return db.ImagePreset{}, false
}

func presetForm(name string, steps int) url.Values {
	return url.Values{
		"name": {name}, "style_suffix": {"ink wash"}, "negative_prompt": {"color"},
		"steps": {fmt.Sprint(steps)}, "cfg_scale": {"6.5"}, "width": {"640"}, "height": {"832"},
		"sampler_name": {"DDIM"}, "seed": {"77"},
	}
}

func TestSettingsImagePresetCRUD(t *testing.T) {
	f := newFixture(t)
	h := SettingsHandlers{DB: f.db}

	// Create.
	w := postForm(h.ImagePresetCreate, 0, presetForm("Sumi-e", 33))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	p, ok := presetByName(t, f, "Sumi-e")
	want := db.ImagePreset{ID: p.ID, Name: "Sumi-e", StyleSuffix: "ink wash", NegativePrompt: "color", Steps: 33, CFGScale: 6.5, Width: 640, Height: 832, SamplerName: "DDIM", Seed: 77}
	if !ok || p != want {
		t.Fatalf("created = %+v, want %+v", p, want)
	}

	// Settings lists it, and the edit URL opens the form in edit mode.
	r := httptest.NewRequest("GET", "/settings", nil)
	page := httptest.NewRecorder()
	h.List(page, r)
	if !strings.Contains(page.Body.String(), fmt.Sprintf(`href="/settings/image-presets/%d/edit"`, p.ID)) {
		t.Fatal("Settings page doesn't list the new preset")
	}
	r = httptest.NewRequest("GET", "/x", nil)
	r.SetPathValue("id", fmt.Sprint(p.ID))
	page = httptest.NewRecorder()
	h.ImagePresetEditForm(page, r)
	body := page.Body.String()
	if !strings.Contains(body, fmt.Sprintf(`action="/settings/image-presets/%d"`, p.ID)) || !strings.Contains(body, `value="ink wash"`) || !strings.Contains(body, `id="image-preset-modal" open`) {
		t.Fatalf("edit form not rendered open in edit mode with the preset's values")
	}

	// Update.
	w = postForm(h.ImagePresetUpdate, p.ID, presetForm("Sumi-e v2", 44))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	if got, err := db.GetImagePreset(f.db, p.ID); err != nil || got.Name != "Sumi-e v2" || got.Steps != 44 {
		t.Fatalf("after update: %+v %v", got, err)
	}

	// Same-name collision (case-insensitive, against a seeded preset):
	// visible error, submitted values kept, nothing written.
	cyber, _ := presetByName(t, f, "Cyberpunk")
	for name, post := range map[string]func() *httptest.ResponseRecorder{
		"create": func() *httptest.ResponseRecorder {
			return postForm(h.ImagePresetCreate, 0, presetForm("cyberpunk", 99))
		},
		"update": func() *httptest.ResponseRecorder {
			return postForm(h.ImagePresetUpdate, p.ID, presetForm("CYBERPUNK", 99))
		},
	} {
		w = post()
		body := w.Body.String()
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(body, `class="danger-text"`) || !strings.Contains(body, "already exists") || !strings.Contains(body, `value="99"`) {
			t.Fatalf("%s collision: code %d, want 422 + visible error + submitted values", name, w.Code)
		}
	}
	if got, _ := db.GetImagePreset(f.db, cyber.ID); got != cyber {
		t.Fatalf("collision clobbered the existing preset: %+v → %+v", cyber, got)
	}
	if got, _ := db.GetImagePreset(f.db, p.ID); got.Name != "Sumi-e v2" || got.Steps != 44 {
		t.Fatalf("rejected update still wrote: %+v", got)
	}

	// Out-of-range field: refused the same visible way.
	bad := presetForm("Huge", 20)
	bad.Set("width", "4096")
	if w = postForm(h.ImagePresetCreate, 0, bad); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "width must be") {
		t.Fatalf("bad width: %d", w.Code)
	}
	if _, ok := presetByName(t, f, "Huge"); ok {
		t.Fatal("invalid preset was saved")
	}

	// Delete.
	if w = postForm(h.ImagePresetDelete, p.ID, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("delete: %d", w.Code)
	}
	if _, err := db.GetImagePreset(f.db, p.ID); err == nil {
		t.Fatal("preset still there after delete")
	}
}

func TestGenerateImageUsesSessionPreset(t *testing.T) {
	f := newFixture(t)
	t.Chdir(t.TempDir())
	srv, received := mockA1111(t, tinyPNGBase64)
	profileID, _ := db.CreateBackendProfile(f.db, db.BackendProfile{Name: "sd", Kind: "image", BaseURL: srv.URL})
	chat := ChatHandlers{DB: f.db}
	generate := func(step string) imageclient.GenerationParams {
		t.Helper()
		if w := postForm(chat.GenerateImage, f.session, imageForm(profileID)); w.Header().Get("HX-Redirect") == "" {
			t.Fatalf("%s: generation failed: %s", step, w.Body.String())
		}
		return received()
	}
	presetCol := func() sql.NullInt64 {
		t.Helper()
		var v sql.NullInt64
		if err := f.db.QueryRow(`SELECT image_preset_id FROM sessions WHERE id = ?`, f.session).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	defaults := imageclient.GenerationParams{Prompt: "a castle on a hill", Steps: 20, CFGScale: 7, Width: 512, Height: 512, SamplerName: "Euler a", Seed: -1}

	// No preset chosen → defaultImageParams.
	if got := generate("no preset"); got != defaults {
		t.Fatalf("no preset: backend got %+v, want %+v", got, defaults)
	}

	// Pick one through the session panel's handler → persisted.
	preset := db.ImagePreset{Name: "Noir", StyleSuffix: "film noir, black and white", NegativePrompt: "color", Steps: 41, CFGScale: 5.5, Width: 768, Height: 1024, SamplerName: "DDIM", Seed: 1234}
	preset.ID, _ = db.CreateImagePreset(f.db, preset)
	if w := postForm(chat.SetImagePreset, f.session, url.Values{"image_preset_id": {fmt.Sprint(preset.ID)}}); w.Code != http.StatusSeeOther {
		t.Fatalf("SetImagePreset: %d %s", w.Code, w.Body.String())
	}
	if v := presetCol(); !v.Valid || v.Int64 != preset.ID {
		t.Fatalf("sessions.image_preset_id = %+v, want %d", v, preset.ID)
	}

	// The panel shows it selected; the dialog names it.
	r := httptest.NewRequest("GET", "/x", nil)
	r.SetPathValue("id", fmt.Sprint(f.session))
	page := httptest.NewRecorder()
	chat.Show(page, r)
	if !strings.Contains(page.Body.String(), fmt.Sprintf(`<option value="%d" selected>Noir</option>`, preset.ID)) || !strings.Contains(page.Body.String(), "Using preset “Noir”") {
		t.Fatal("chat page doesn't show Noir as the session's selected/active preset")
	}

	// Generation uses exactly the preset's fields.
	want := imageclient.GenerationParams{Prompt: "a castle on a hill, film noir, black and white", NegativePrompt: "color", Steps: 41, CFGScale: 5.5, Width: 768, Height: 1024, SamplerName: "DDIM", Seed: 1234}
	if got := generate("with preset"); got != want {
		t.Fatalf("with preset: backend got %+v, want %+v", got, want)
	}

	// An unknown preset ID is refused, leaving the choice alone.
	if w := postForm(chat.SetImagePreset, f.session, url.Values{"image_preset_id": {"999999"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown preset: %d", w.Code)
	}
	if v := presetCol(); v.Int64 != preset.ID {
		t.Fatalf("refused pick changed the row: %+v", v)
	}

	// Deleting the active preset in Settings → NULL, and the next
	// generation quietly uses defaults.
	if w := postForm(SettingsHandlers{DB: f.db}.ImagePresetDelete, preset.ID, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("delete: %d", w.Code)
	}
	if v := presetCol(); v.Valid {
		t.Fatalf("image_preset_id = %d after preset delete, want NULL", v.Int64)
	}
	if got := generate("after delete"); got != defaults {
		t.Fatalf("after delete: backend got %+v, want %+v", got, defaults)
	}

	// Picking "(Default)" stores NULL, not 0.
	other, _ := db.CreateImagePreset(f.db, db.ImagePreset{Name: "Other", Steps: 20, CFGScale: 7, Width: 512, Height: 512, SamplerName: "Euler a", Seed: -1})
	db.SetSessionImagePreset(f.db, f.session, other)
	postForm(chat.SetImagePreset, f.session, url.Values{"image_preset_id": {"0"}})
	if v := presetCol(); v.Valid {
		t.Fatalf("(Default) stored %d, want NULL", v.Int64)
	}
}

// mockLLM answers chat/completions SSE with reply token-by-token (one
// token per rune group isn't needed — a single data: line is enough
// for these tests) and reports the last request's decoded messages.
func mockLLM(t *testing.T, reply string) (*httptest.Server, func() []llmclient.Message) {
	t.Helper()
	var (
		mu   sync.Mutex
		last []llmclient.Message
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []llmclient.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		last = body.Messages
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", reply)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv, func() []llmclient.Message {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
}

// TestGenerateImageUnloadsCheckpointAfterSuccess is Lord's VRAM
// complaint: after an image finishes, Forge used to keep the
// checkpoint loaded forever, starving llama.cpp on the next chat
// message. GenerateImage must call the backend's unload-checkpoint
// endpoint once the image is safely saved.
func TestGenerateImageUnloadsCheckpointAfterSuccess(t *testing.T) {
	f := newFixture(t)
	t.Chdir(t.TempDir())
	var unloadCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sdapi/v1/unload-checkpoint" {
			atomic.AddInt32(&unloadCalls, 1)
			json.NewEncoder(w).Encode(map[string]any{})
			return
		}
		info, _ := json.Marshal(map[string]any{"seed": 4242})
		json.NewEncoder(w).Encode(map[string]any{"images": []string{tinyPNGBase64}, "info": string(info)})
	}))
	t.Cleanup(srv.Close)
	profileID, _ := db.CreateBackendProfile(f.db, db.BackendProfile{Name: "sd", Kind: "image", BaseURL: srv.URL})
	h := ChatHandlers{DB: f.db}

	if w := postForm(h.GenerateImage, f.session, imageForm(profileID)); w.Header().Get("HX-Redirect") == "" {
		t.Fatalf("generation failed: %s", w.Body.String())
	}
	if n := atomic.LoadInt32(&unloadCalls); n != 1 {
		t.Fatalf("unload-checkpoint called %d times, want exactly 1", n)
	}
}

// TestGenerateImageUnloadFailureDoesntFailTheRequest: unload is
// best-effort — the image already generated and saved successfully,
// so a broken/old backend without this endpoint must not turn a good
// generation into a user-facing failure.
func TestGenerateImageUnloadFailureDoesntFailTheRequest(t *testing.T) {
	f := newFixture(t)
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sdapi/v1/unload-checkpoint" {
			http.Error(w, "no such endpoint", http.StatusNotFound)
			return
		}
		info, _ := json.Marshal(map[string]any{"seed": 4242})
		json.NewEncoder(w).Encode(map[string]any{"images": []string{tinyPNGBase64}, "info": string(info)})
	}))
	t.Cleanup(srv.Close)
	profileID, _ := db.CreateBackendProfile(f.db, db.BackendProfile{Name: "sd", Kind: "image", BaseURL: srv.URL})
	h := ChatHandlers{DB: f.db}

	if w := postForm(h.GenerateImage, f.session, imageForm(profileID)); w.Header().Get("HX-Redirect") == "" {
		t.Fatalf("a 404 on unload must not fail the already-successful generation: %s", w.Body.String())
	}
}

// TestDemoModeDisablesGeneration is Lord's demo-build request: with
// DemoMode on, both image endpoints must refuse without ever reaching
// a real backend — t.Cleanup resets the package var so this doesn't
// leak into other tests run in the same binary.
func TestDemoModeDisablesGeneration(t *testing.T) {
	DemoMode = true
	t.Cleanup(func() { DemoMode = false })

	f := newFixture(t)
	profileID, _ := db.CreateBackendProfile(f.db, db.BackendProfile{Name: "sd", Kind: "image", BaseURL: "http://127.0.0.1:1"})
	h := ChatHandlers{DB: f.db}

	w := postForm(h.GenerateImage, f.session, imageForm(profileID))
	if w.Header().Get("HX-Redirect") != "" || !strings.Contains(w.Body.String(), "disabled in this demo build") {
		t.Fatalf("GenerateImage under DemoMode: redirect %q, body %s", w.Header().Get("HX-Redirect"), w.Body.String())
	}
	if n := f.count(t, `SELECT COUNT(*) FROM messages WHERE session_id = ? AND role = 'image'`, f.session); n != 0 {
		t.Fatalf("DemoMode still created an image message: %d", n)
	}

	w = postForm(h.SuggestImagePrompt, f.session, nil)
	if !strings.Contains(w.Body.String(), "disabled in this demo build") {
		t.Fatalf("SuggestImagePrompt under DemoMode: body %s", w.Body.String())
	}

	// The button itself renders disabled too — not just the endpoint.
	r := httptest.NewRequest("GET", "/x", nil)
	r.SetPathValue("id", fmt.Sprint(f.session))
	page := httptest.NewRecorder()
	h.Show(page, r)
	if !strings.Contains(page.Body.String(), `id="generate-image-btn"`) {
		t.Fatal("the Generate image button must still be present (visible, just inert)")
	}
	if !regexp.MustCompile(`id="generate-image-btn"[^>]*\bdisabled\b`).MatchString(page.Body.String()) {
		t.Fatal("the Generate image button must render disabled under DemoMode")
	}
}

// TestSuggestImagePromptUsesLLMAndSubstitutesMacros is Lord's "one
// click" request: the session's own LLM backend writes a candidate
// prompt, and the character context it's given must already have
// {{user}}/{{char}} resolved — a macro leak into this side-channel
// request would be just as wrong as leaking into the chat itself.
func TestSuggestImagePromptUsesLLMAndSubstitutesMacros(t *testing.T) {
	f := newFixture(t)
	if err := db.UpdateCharacterCard(f.db, db.CharacterCard{ID: f.cardA, Name: "Merlin", Description: "{{char}} watches over {{user}}."}); err != nil {
		t.Fatal(err)
	}
	srv, received := mockLLM(t, "an old wizard in a stone tower, warm candlelight, detailed robes")
	llmProfile, err := db.CreateBackendProfile(f.db, db.BackendProfile{Name: "chat-llm", Kind: "llm", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE sessions SET backend_profile_id = ? WHERE id = ?`, llmProfile, f.session); err != nil {
		t.Fatal(err)
	}

	h := ChatHandlers{DB: f.db}
	w := postForm(h.SuggestImagePrompt, f.session, nil)
	body := w.Body.String()
	if !strings.Contains(body, "an old wizard in a stone tower, warm candlelight, detailed robes") {
		t.Fatalf("suggested prompt missing from the response fragment: %s", body)
	}
	if !strings.Contains(body, `id="image-prompt-field"`) {
		t.Fatalf("response isn't the swappable fragment: %s", body)
	}

	msgs := received()
	if len(msgs) < 2 {
		t.Fatalf("expected at least a system+user message sent to the LLM, got %+v", msgs)
	}
	joined := msgs[0].Content + "\n" + msgs[1].Content
	if strings.Contains(joined, "{{user}}") || strings.Contains(joined, "{{char}}") {
		t.Fatalf("a literal macro leaked into the prompt-writing LLM request:\n%s", joined)
	}
	if !strings.Contains(joined, "Merlin watches over Hero.") {
		t.Fatalf("substituted character text missing from the LLM request:\n%s", joined)
	}
	if !strings.Contains(joined, "hello") { // f.cardA's "hello" message, seeded by newFixture
		t.Fatalf("recent conversation missing from the LLM request:\n%s", joined)
	}
}

// TestGenerateImageUsesPresetLinkedBackend is Lord's exact request:
// once a preset is tied to a backend (ImagePreset.BackendProfileID),
// that link wins over whatever the dialog's form posts — simulating a
// stale/manual pick of a DIFFERENT image backend to prove the preset
// really overrides it, not just happens to agree with it.
func TestGenerateImageUsesPresetLinkedBackend(t *testing.T) {
	f := newFixture(t)
	t.Chdir(t.TempDir())
	realSrv, realReceived := mockA1111(t, tinyPNGBase64)
	staleSrv, staleReceived := mockA1111(t, tinyPNGBase64)
	real, _ := db.CreateBackendProfile(f.db, db.BackendProfile{Name: "Forge-Juggernaut", Kind: "image", BaseURL: realSrv.URL, Model: "Juggernaut-XL_v9.safetensors"})
	stale, _ := db.CreateBackendProfile(f.db, db.BackendProfile{Name: "Forge-Pony", Kind: "image", BaseURL: staleSrv.URL, Model: "ponyDiffusionV6XL.safetensors"})

	preset := db.ImagePreset{Name: "Realistic2", Steps: 30, CFGScale: 6, Width: 512, Height: 768, SamplerName: "DPM++ 2M Karras", Seed: -1, BackendProfileID: real}
	preset.ID, _ = db.CreateImagePreset(f.db, preset)
	if err := db.SetSessionImagePreset(f.db, f.session, preset.ID); err != nil {
		t.Fatal(err)
	}

	// The form still names the OTHER (stale) backend — as a leftover
	// hidden/selected value from before the preset was linked — and it
	// must be ignored.
	chat := ChatHandlers{DB: f.db}
	if w := postForm(chat.GenerateImage, f.session, imageForm(stale)); w.Header().Get("HX-Redirect") == "" {
		t.Fatalf("generation failed: %s", w.Body.String())
	}

	if got := realReceived(); got.Steps != 30 || got.SamplerName != "DPM++ 2M Karras" {
		t.Fatalf("the preset's own backend (Forge-Juggernaut) never got the request: %+v", got)
	}
	if got := staleReceived(); got.Prompt != "" {
		t.Fatalf("the stale form backend (Forge-Pony) got hit instead of being overridden: %+v", got)
	}
}

// The fallback also covers a preset row that vanishes without the FK
// firing (e.g. foreign_keys off on some connection).
func TestGenerateImageMissingPresetFallsBack(t *testing.T) {
	f := newFixture(t)
	t.Chdir(t.TempDir())
	srv, received := mockA1111(t, tinyPNGBase64)
	profileID, _ := db.CreateBackendProfile(f.db, db.BackendProfile{Name: "sd", Kind: "image", BaseURL: srv.URL})
	// PRAGMA is per-connection: pin one, and re-enable FKs before it
	// goes back to the pool.
	conn, err := f.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	conn.ExecContext(context.Background(), `PRAGMA foreign_keys = OFF`)
	_, err = conn.ExecContext(context.Background(), `UPDATE sessions SET image_preset_id = 987654 WHERE id = ?`, f.session)
	conn.ExecContext(context.Background(), `PRAGMA foreign_keys = ON`)
	conn.Close()
	if err != nil {
		t.Fatal(err)
	}
	if w := postForm(ChatHandlers{DB: f.db}.GenerateImage, f.session, imageForm(profileID)); w.Header().Get("HX-Redirect") == "" {
		t.Fatalf("generation failed: %s", w.Body.String())
	}
	if got := received(); got.Steps != 20 || got.SamplerName != "Euler a" || got.Prompt != "a castle on a hill" {
		t.Fatalf("backend got %+v, want defaults", got)
	}
}
