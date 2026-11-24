package imageclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

// tinyPNGBase64 is a valid 1×1 PNG.
const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

func TestGenerateImageSuccess(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sdapi/v1/txt2img" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&got)
		info, _ := json.Marshal(map[string]any{"seed": 424242, "steps": 20})
		json.NewEncoder(w).Encode(map[string]any{"images": []string{tinyPNGBase64}, "info": string(info)})
	}))
	defer srv.Close()

	profile := db.BackendProfile{Kind: "image", BaseURL: srv.URL + "/", APIKey: "k", Model: "sdxl.safetensors"}
	res, err := GenerateImage(context.Background(), profile, GenerationParams{
		Prompt: "a castle", NegativePrompt: "blurry", Steps: 20, CFGScale: 7, Width: 512, Height: 768, SamplerName: "Euler a", Seed: -1,
	})
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	want, _ := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if !bytes.Equal(res.Image, want) {
		t.Fatalf("image bytes differ")
	}
	if res.Seed != 424242 {
		t.Fatalf("seed = %d, want the one echoed back in info", res.Seed)
	}
	if auth != "Bearer k" {
		t.Fatalf("Authorization = %q", auth)
	}
	for k, v := range map[string]any{"prompt": "a castle", "negative_prompt": "blurry", "steps": 20.0, "cfg_scale": 7.0, "width": 512.0, "height": 768.0, "sampler_name": "Euler a", "seed": -1.0} {
		if got[k] != v {
			t.Errorf("request %s = %v, want %v", k, got[k], v)
		}
	}
	if ov, _ := got["override_settings"].(map[string]any); ov["sd_model_checkpoint"] != "sdxl.safetensors" {
		t.Errorf("override_settings = %v", got["override_settings"])
	}
}

func TestGenerateImageHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not loaded", http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := GenerateImage(context.Background(), db.BackendProfile{BaseURL: srv.URL}, GenerationParams{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "model not loaded") {
		t.Fatalf("err = %v", err)
	}
}

func TestGenerateImageMalformed(t *testing.T) {
	for name, body := range map[string]string{
		"images not an array": `{"images": "nope", "info": "{}"}`,
		"empty images array":  `{"images": [], "info": "{}"}`,
		"images not base64":   `{"images": ["!!!not base64!!!"], "info": "{}"}`,
		"not JSON at all":     `<html>oops</html>`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(body))
			}))
			defer srv.Close()
			if _, err := GenerateImage(context.Background(), db.BackendProfile{BaseURL: srv.URL}, GenerationParams{Prompt: "x"}); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestGenerateImageBadInfoKeepsRequestedSeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"images": ["` + tinyPNGBase64 + `"], "info": "not json"}`))
	}))
	defer srv.Close()
	res, err := GenerateImage(context.Background(), db.BackendProfile{BaseURL: srv.URL}, GenerationParams{Prompt: "x", Seed: 7})
	if err != nil || res.Seed != 7 {
		t.Fatalf("res.Seed=%d err=%v", res.Seed, err)
	}
}

// TestUnloadCheckpoint is the VRAM-release call GenerateImage's caller
// fires after a successful generation (Lord's report: Forge used to
// hold the checkpoint in VRAM indefinitely, starving llama.cpp on the
// next chat turn). Confirmed for real against Lord's live Forge
// instance before writing this (POST /sdapi/v1/unload-checkpoint, ~
// 7.1 GB actually freed) — this test just locks in the request shape.
func TestUnloadCheckpoint(t *testing.T) {
	var gotPath, gotMethod, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotAuth = r.URL.Path, r.Method, r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()

	err := UnloadCheckpoint(context.Background(), db.BackendProfile{BaseURL: srv.URL + "/", APIKey: "k"})
	if err != nil {
		t.Fatalf("UnloadCheckpoint: %v", err)
	}
	if gotPath != "/sdapi/v1/unload-checkpoint" || gotMethod != http.MethodPost {
		t.Fatalf("got %s %s", gotMethod, gotPath)
	}
	if gotAuth != "Bearer k" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}

func TestUnloadCheckpointHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no model loaded", http.StatusBadRequest)
	}))
	defer srv.Close()
	err := UnloadCheckpoint(context.Background(), db.BackendProfile{BaseURL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("err = %v", err)
	}
}
