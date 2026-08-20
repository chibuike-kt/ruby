package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/chibuike-kt/ruby/internal/ai"
)

// Transcriber implements ai.Transcriber against POST
// /v1/audio/transcriptions using gpt-transcribe.
//
// No language hint is sent (docs/BRIEF-fixes-and-reminders.md #2): an
// earlier version wrote a repeated "languages" field, assumed from docs
// to be the model's multi-language hint mechanism. Verified against the
// live endpoint while debugging why every voice note failed — that
// field (in any form: repeated "languages", or "languages[]") gets a
// flat 400 invalid_request_error/invalid_value, every time, regardless
// of audio content. Omitting it entirely works: the model auto-detects
// correctly on its own.
//
// The response's language info also isn't the singular "language"
// string field the same earlier assumption expected — the live
// endpoint returns "languages": [{"code": "en"}], an array (see
// transcriptionResponse).
type Transcriber struct {
	apiKey string
}

func NewTranscriber(apiKey string) *Transcriber {
	return &Transcriber{apiKey: apiKey}
}

type transcriptionResponse struct {
	Text      string                    `json:"text"`
	Languages []transcriptionLanguageID `json:"languages"`
}

type transcriptionLanguageID struct {
	Code string `json:"code"`
}

func (t *Transcriber) Transcribe(ctx context.Context, audio []byte) (string, ai.Language, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)

	fw, err := w.CreateFormFile("file", "voice-note.mp3")
	if err != nil {
		return "", "", err
	}
	if _, err := fw.Write(audio); err != nil {
		return "", "", err
	}
	if err := w.WriteField("model", transcriptionModel); err != nil {
		return "", "", err
	}
	if err := w.Close(); err != nil {
		return "", "", err
	}

	bodyBytes := body.Bytes()
	contentType := w.FormDataContentType()
	resp, err := doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/audio/transcriptions", bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Authorization", "Bearer "+t.apiKey)
		return req, nil
	})
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", apiRequestError(resp.StatusCode, respBody)
	}

	var parsed transcriptionResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", "", err
	}
	var detected ai.Language
	if len(parsed.Languages) > 0 {
		detected = ai.Language(parsed.Languages[0].Code)
	}
	return parsed.Text, detected, nil
}
