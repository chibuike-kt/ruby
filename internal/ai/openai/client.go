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

// retryBackoff is docs/BRIEF-polish-and-hardening.md #4's outbound
// retry/backoff requirement: up to len(retryBackoff) retries (3
// attempts total) on a transient failure — a network error, 429, or
// 5xx — each waiting a little longer than the last. A genuine client
// error (any other 4xx, e.g. a bad request or an auth failure) is
// never retried: retrying can't fix a malformed request or a bad key.
var retryBackoff = []time.Duration{200 * time.Millisecond, 500 * time.Millisecond}

// doWithRetry executes newReq's request, retrying on a transient
// failure. newReq is called fresh on every attempt (an http.Request's
// body reader is single-use, so a retry needs a brand new one, not the
// same request replayed).
func doWithRetry(ctx context.Context, newReq func() (*http.Request, error)) (*http.Response, error) {
	var lastErr error
	for attempt := 0; ; attempt++ {
		req, err := newReq()
		if err != nil {
			return nil, err
		}
		resp, err := httpClient.Do(req)
		if err == nil && resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return resp, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("openai: transient failure with status %d", resp.StatusCode)
			_ = resp.Body.Close()
		}
		if attempt >= len(retryBackoff) {
			return nil, lastErr
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retryBackoff[attempt]):
		}
	}
}

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

	resp, err := doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/responses", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)
		return req, nil
	})
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
