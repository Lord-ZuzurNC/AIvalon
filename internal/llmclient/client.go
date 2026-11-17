// Package llmclient talks to a BackendProfile's OpenAI-compatible
// chat/completions endpoint. It never embeds inference itself — see
// docs/DESIGN.md ("Backend profiles").
package llmclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// StreamChat posts messages to profile's chat/completions endpoint with
// stream=true and calls onToken for each text delta as it arrives.
//
// Cancelling ctx aborts the outbound HTTP request — this is the other half
// of docs/DESIGN.md's "Stopping a generation in progress": the caller ties
// ctx to the browser's closed SSE connection, and that's the only wiring
// needed for the backend to actually stop generating instead of just losing
// its audience.
func StreamChat(ctx context.Context, profile db.BackendProfile, messages []Message, onToken func(string)) error {
	body := map[string]any{"messages": messages, "stream": true}
	if profile.Model != "" {
		body["model"] = profile.Model
	}
	for k, v := range profile.ModelParams {
		body[k] = v
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encoding request: %w", err)
	}

	url := strings.TrimRight(profile.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if profile.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+profile.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling backend: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// The backend is whatever base_url the user configured —
		// trusted to be an LLM API, but not trusted to cap its own
		// error response size. io.LimitReader keeps a misbehaving or
		// malicious backend from making this read (and the resulting
		// error string, which flows straight into the chat's SSE
		// error event) unbounded.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("backend returned %s: %s", resp.Status, b)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // a malformed/heartbeat line shouldn't abort an otherwise-good stream
		}
		for _, c := range chunk.Choices {
			if c.Delta.Content != "" {
				onToken(c.Delta.Content)
			}
		}
	}
	return scanner.Err()
}
