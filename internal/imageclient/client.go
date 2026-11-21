// Package imageclient talks to a BackendProfile (kind "image") that
// generates pictures. v1 speaks only Automatic1111's (Stable Diffusion
// WebUI, launched with --api) txt2img endpoint. GenerateImage is the one
// entry point callers use — a second provider (a hosted API) slots in
// later as a switch on the profile inside it, without touching callers.
package imageclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

// generateTimeout is far longer than backend_health.go's testTimeout:
// a single txt2img on CPU (or a cold checkpoint load) can take minutes.
const generateTimeout = 5 * time.Minute

// maxResponseBytes caps the decoded JSON body — a base64 PNG at 1024²
// is a few MiB; this leaves headroom without letting a misbehaving
// backend stream unbounded data into memory.
const maxResponseBytes = 64 << 20

var httpClient = &http.Client{Timeout: generateTimeout}

// GenerationParams' JSON tags are A1111's own txt2img field names.
type GenerationParams struct {
	Prompt         string  `json:"prompt"`
	NegativePrompt string  `json:"negative_prompt"`
	Steps          int     `json:"steps"`
	CFGScale       float64 `json:"cfg_scale"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	SamplerName    string  `json:"sampler_name"`
	Seed           int64   `json:"seed"` // -1 = random
}

type GenerationResult struct {
	Image []byte // raw image bytes (PNG from A1111)
	Seed  int64  // the seed actually used — resolved by the backend when -1 was asked for
}

func GenerateImage(ctx context.Context, profile db.BackendProfile, params GenerationParams) (GenerationResult, error) {
	body := struct {
		GenerationParams
		OverrideSettings map[string]string `json:"override_settings,omitempty"`
	}{GenerationParams: params}
	if profile.Model != "" {
		// A1111 restores its previously loaded checkpoint afterwards by
		// default (override_settings_restore_afterwards=true).
		body.OverrideSettings = map[string]string{"sd_model_checkpoint": profile.Model}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return GenerationResult{}, fmt.Errorf("encoding request: %w", err)
	}

	url := strings.TrimRight(profile.BaseURL, "/") + "/sdapi/v1/txt2img"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return GenerationResult{}, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if profile.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+profile.APIKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return GenerationResult{}, fmt.Errorf("calling image backend: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Same bounded-error-body rule as llmclient.StreamChat.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return GenerationResult{}, fmt.Errorf("image backend returned %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	var out struct {
		Images []string `json:"images"`
		Info   string   `json:"info"` // itself a JSON document, encoded as a string
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&out); err != nil {
		return GenerationResult{}, fmt.Errorf("decoding image backend response: %w", err)
	}
	if len(out.Images) == 0 {
		return GenerationResult{}, fmt.Errorf("image backend returned no images")
	}
	img, err := base64.StdEncoding.DecodeString(out.Images[0])
	if err != nil {
		return GenerationResult{}, fmt.Errorf("decoding image base64: %w", err)
	}

	result := GenerationResult{Image: img, Seed: params.Seed}
	var info struct {
		Seed *int64 `json:"seed"`
	}
	// A missing/unparseable info isn't worth failing a good image over —
	// the seed just stays what was requested.
	if json.Unmarshal([]byte(out.Info), &info) == nil && info.Seed != nil {
		result.Seed = *info.Seed
	}
	return result, nil
}
