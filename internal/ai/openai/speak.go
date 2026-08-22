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
const speechVoice = "nova"

// speechResponseFormat and speechMimeType are OGG/Opus, not AAC —
// docs/BRIEF-research-hardening-standard.md Part 5 live-testing: AAC
// uploads fine but WhatsApp only renders a genuine voice-note bubble
// (waveform, inline playback) for audio/ogg with the Opus codec; any
// other format, AAC included, displays as a plain downloadable file
// attachment, defeating the point of a voice reply. Verified live
// (2026-08-22) that response_format=opus from /v1/audio/speech returns
// a real Ogg container — the response starts with the "OggS" magic
// bytes followed by an OpusHead chunk, not bare Opus frames — so these
// bytes are passed straight through to WhatsApp with no repackaging
// step (unlike the inbound direction, internal/ai/ffmpeg, which exists
// only because gpt-transcribe can't accept Ogg/Opus itself).
const speechResponseFormat = "opus"
const speechMimeType = "audio/ogg; codecs=opus"

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
		ResponseFormat: speechResponseFormat,
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
