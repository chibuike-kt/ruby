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
// /v1/audio/transcriptions using gpt-transcribe. langHints populate the
// current API's "languages" (plural, array) hint field — verified
// against current docs during planning, not the older singular
// "language" parameter.
type Transcriber struct {
	apiKey string
}

func NewTranscriber(apiKey string) *Transcriber {
	return &Transcriber{apiKey: apiKey}
}

type transcriptionResponse struct {
	Text     string `json:"text"`
	Language string `json:"language"`
}

func (t *Transcriber) Transcribe(ctx context.Context, audio []byte, langHints []ai.Language) (string, ai.Language, error) {
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
	for _, lang := range langHints {
		if err := w.WriteField("languages", string(lang)); err != nil {
			return "", "", err
		}
	}
	if err := w.Close(); err != nil {
		return "", "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/audio/transcriptions", &body)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	resp, err := httpClient.Do(req)
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
	return parsed.Text, ai.Language(parsed.Language), nil
}
