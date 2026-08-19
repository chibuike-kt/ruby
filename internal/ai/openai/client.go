// Package openai implements internal/ai's Extractor/Transcriber/Phraser
// interfaces against OpenAI's API. Every request/response shape here was
// verified against current docs during planning (not assumed from older
// JSON-mode/whisper-1 conventions) — see docs/BRIEF-ai-intent.md.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// baseURL is a var, not a const, so tests can point it at an
// httptest.Server instead of OpenAI's real endpoint.
var baseURL = "https://api.openai.com/v1"

var httpClient = &http.Client{Timeout: 60 * time.Second}

// transcriptionModel and phrasingModel are fixed, not configurable —
// docs/BRIEF-ai-intent.md already made these calls (gpt-transcribe is
// OpenAI's current recommended default; gpt-5.6-luna is the deliberately
// cheap tier for a phrasing-only call). The extraction model is the one
// knob left open, via config.AIModel — see NewExtractor.
const transcriptionModel = "gpt-transcribe"
const phrasingModel = "gpt-5.6-luna"

// maxResponseBytes caps how much of any OpenAI response body this
// client reads into memory — generously above what a JSON response
// needs, well below what a runaway response could do.
const maxResponseBytes = 1 << 20

type apiError struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// responsesRequest/responsesMessage/responsesText/responsesFormat match
// POST /v1/responses' current shape, verified against docs during
// planning: text.format = {type:"json_schema", name, strict, schema}
// for Structured Outputs strict mode; Text nil means plain-text output
// (used by the phrasing call, which doesn't need a schema).
type responsesRequest struct {
	Model string             `json:"model"`
	Input []responsesMessage `json:"input"`
	Text  *responsesText     `json:"text,omitempty"`
}

type responsesMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responsesText struct {
	Format responsesFormat `json:"format"`
}

type responsesFormat struct {
	Type   string         `json:"type"`
	Name   string         `json:"name,omitempty"`
	Strict bool           `json:"strict,omitempty"`
	Schema map[string]any `json:"schema,omitempty"`
}

type responsesResponse struct {
	Output []responsesOutput `json:"output"`
	Status string            `json:"status"`
}

type responsesOutput struct {
	Type    string             `json:"type"`
	Content []responsesContent `json:"content"`
}

type responsesContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// postResponses posts to /v1/responses and returns the first
// output_text content block. Shared by Extractor (strict schema) and
// Phraser (plain text) — only the request body differs between them.
func postResponses(ctx context.Context, apiKey string, reqBody responsesRequest) (string, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", apiRequestError(resp.StatusCode, respBody)
	}

	var parsed responsesResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", err
	}
	for _, out := range parsed.Output {
		for _, c := range out.Content {
			if c.Type == "output_text" && c.Text != "" {
				return c.Text, nil
			}
		}
	}
	return "", errors.New("openai: response had no output_text content")
}

func apiRequestError(status int, body []byte) error {
	var apiErr apiError
	_ = json.Unmarshal(body, &apiErr)
	if apiErr.Error.Message != "" {
		return fmt.Errorf("openai: request failed with status %d: %s", status, apiErr.Error.Message)
	}
	return fmt.Errorf("openai: request failed with status %d", status)
}
