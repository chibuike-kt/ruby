package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// graphAPIBaseURL is the WhatsApp Cloud (Graph) API's base URL. It's a
// var, not a const, so tests can point it at an httptest.Server instead
// of Meta's real endpoint.
var graphAPIBaseURL = "https://graph.facebook.com/v26.0"

var httpClient = &http.Client{Timeout: 20 * time.Second}

// maxMediaResponseBytes caps how much of any Graph API response body
// this client reads into memory — generously above what a JSON metadata
// response needs, well below what a runaway/malicious response could do.
const maxMediaResponseBytes = 1 << 20

// maxMediaBytes matches gpt-transcribe's own 25MB cap (docs/BRIEF-ai-intent.md)
// — no point buffering a voice note the transcription call would reject anyway.
const maxMediaBytes = 25 << 20

type sendTextRequest struct {
	MessagingProduct string          `json:"messaging_product"`
	To               string          `json:"to"`
	Type             string          `json:"type"`
	Text             sendTextPayload `json:"text"`
}

type sendTextPayload struct {
	Body string `json:"body"`
}

type sendMessageResponse struct {
	Messages []struct {
		ID string `json:"id"`
	} `json:"messages"`
}

// sendText posts a text message via the Cloud API and returns the
// provider-assigned message id from the response. `to` is normalized to
// the Cloud API's digits-only convention (the inverse of normalizePhone,
// which adds "+" for our own storage format).
func sendText(ctx context.Context, accessToken, phoneNumberID, to, body string) (string, error) {
	reqBody, err := json.Marshal(sendTextRequest{
		MessagingProduct: "whatsapp",
		To:               strings.TrimPrefix(to, "+"),
		Type:             "text",
		Text:             sendTextPayload{Body: body},
	})
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/%s/messages", graphAPIBaseURL, phoneNumberID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxMediaResponseBytes))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("whatsapp: send failed with status %d", resp.StatusCode)
	}

	var parsed sendMessageResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Messages) == 0 {
		return "", fmt.Errorf("whatsapp: send response had no message id")
	}
	return parsed.Messages[0].ID, nil
}

type mediaMetaResponse struct {
	URL      string `json:"url"`
	MimeType string `json:"mime_type"`
}

// downloadMedia resolves mediaID to a temporary download URL (spec §22's
// "WhatsApp Media API" step) and fetches the bytes behind it. Both calls
// need the same bearer token — the lookup because it's a Graph API call,
// the download because Meta's CDN still checks it on the handed-back URL.
func downloadMedia(ctx context.Context, accessToken, mediaID string) ([]byte, string, error) {
	metaURL := fmt.Sprintf("%s/%s", graphAPIBaseURL, mediaID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	metaBody, err := io.ReadAll(io.LimitReader(resp.Body, maxMediaResponseBytes))
	_ = resp.Body.Close()
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("whatsapp: media lookup failed with status %d", resp.StatusCode)
	}

	var meta mediaMetaResponse
	if err := json.Unmarshal(metaBody, &meta); err != nil {
		return nil, "", err
	}
	if meta.URL == "" {
		return nil, "", fmt.Errorf("whatsapp: media lookup returned no url")
	}

	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, meta.URL, nil)
	if err != nil {
		return nil, "", err
	}
	dlReq.Header.Set("Authorization", "Bearer "+accessToken)

	dlResp, err := httpClient.Do(dlReq)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = dlResp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(dlResp.Body, maxMediaBytes))
	if err != nil {
		return nil, "", err
	}
	if dlResp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("whatsapp: media download failed with status %d", dlResp.StatusCode)
	}

	return data, meta.MimeType, nil
}
