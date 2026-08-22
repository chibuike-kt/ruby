package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
)

// speechModel and speechVoice are fixed, not configurable — same
// reasoning as transcriptionModel/phrasingModel (docs/BRIEF-ai-intent.md
// already made this call for the other two AI-provider knobs this
// codebase doesn't expose). gpt-4o-mini-tts is OpenAI's current
// standard text-to-speech model for the regular (non-Realtime)
// /v1/audio/speech endpoint — a one-shot text-to-audio reply, not a
// live streaming voice agent, so the Realtime API is the wrong fit
// here. "gpt-speech" (the original placeholder) was never a real model
// and returned a 404 in production — docs/BRIEF-research-hardening-
// standard.md Part 5 live-testing finding #3's root cause, caught only
// once real Error-level logging was in place.
const speechModel = "gpt-4o-mini-tts"
const speechVoice = "alloy"

// speechMimeType is AAC (docs/BRIEF-research-hardening-standard.md Part
// 5 Tier 1's voice replies): WhatsApp's Cloud API accepts it for an
// outbound audio message, and unlike OGG/Opus it needs no transcoding
// step on the way out — Ruby only transcodes on the way in (see
// internal/ai/ffmpeg), because that direction has no choice: WhatsApp
// voice notes are always OGG/Opus and gpt-transcribe doesn't accept it.
const speechMimeType = "audio/aac"

// maxSpeechBytes matches WhatsApp's own outbound audio message cap (16MB) —
// a reply this codebase phrases is always a short sentence or two, well
// under it, but the read must still be bounded regardless.
const maxSpeechBytes = 16 << 20

// Speaker implements ai.Speaker against POST /v1/audio/speech.
type Speaker struct {
	apiKey string
}

func NewSpeaker(apiKey string) *Speaker {
	return &Speaker{apiKey: apiKey}
}

type speechRequest struct {
	Model          string `json:"model"`
	Input          string `json:"input"`
	Voice          string `json:"voice"`
	ResponseFormat string `json:"response_format"`
}

func (s *Speaker) Speak(ctx context.Context, text string) ([]byte, string, error) {
	reqBody, err := json.Marshal(speechRequest{
		Model:          speechModel,
		Input:          text,
		Voice:          speechVoice,
		ResponseFormat: "aac",
	})
	if err != nil {
		return nil, "", err
	}

	resp, err := doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/audio/speech", bytes.NewReader(reqBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
		return req, nil
	})
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		return nil, "", apiRequestError(resp.StatusCode, errBody)
	}

	audio, err := io.ReadAll(io.LimitReader(resp.Body, maxSpeechBytes))
	if err != nil {
		return nil, "", err
	}
	return audio, speechMimeType, nil
}
