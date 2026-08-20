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

	"github.com/chibuike-kt/ruby/internal/ai"
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

// retryBackoff is docs/BRIEF-polish-and-hardening.md #4's outbound
// retry/backoff requirement: up to len(retryBackoff) retries (3 attempts
// total) on a transient failure — a network error, 429, or 5xx — each
// wait a little longer than the last. A genuine client error (any other
// 4xx) is never retried: retrying can't fix a malformed request, and for
// a message-send call it would risk delivering the same message twice.
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
			lastErr = fmt.Errorf("whatsapp: transient failure with status %d", resp.StatusCode)
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
	return postMessage(ctx, accessToken, phoneNumberID, reqBody)
}

// maxButtonTitleRunes matches docs/BRIEF-interactive-messages.md: 20
// chars avoids on-screen truncation on small phones, even though the
// Cloud API's own hard limit is 25. Enforced here, in the transport
// layer, right before a request leaves the process — the last line of
// defense regardless of what ai handed this function (e.g. a long
// customer name as a button title).
const maxButtonTitleRunes = 20

func truncateTitle(title string) string {
	r := []rune(title)
	if len(r) <= maxButtonTitleRunes {
		return title
	}
	if maxButtonTitleRunes <= 1 {
		return string(r[:maxButtonTitleRunes])
	}
	return string(r[:maxButtonTitleRunes-1]) + "…"
}

type interactiveBodyText struct {
	Text string `json:"text"`
}

type interactiveReply struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type interactiveButtonRequest struct {
	MessagingProduct string                `json:"messaging_product"`
	To               string                `json:"to"`
	Type             string                `json:"type"`
	Interactive      interactiveButtonBody `json:"interactive"`
}

type interactiveButtonBody struct {
	Type   string                  `json:"type"`
	Body   interactiveBodyText     `json:"body"`
	Action interactiveButtonAction `json:"action"`
}

type interactiveButtonAction struct {
	Buttons []interactiveButtonItem `json:"buttons"`
}

type interactiveButtonItem struct {
	Type  string           `json:"type"`
	Reply interactiveReply `json:"reply"`
}

// sendInteractiveButtons posts a reply-buttons interactive message (max
// 3 buttons — spec) via the Cloud API. Titles are truncated to
// maxButtonTitleRunes regardless of what the caller supplied.
func sendInteractiveButtons(ctx context.Context, accessToken, phoneNumberID, to, body string, buttons []ai.Button) (string, error) {
	items := make([]interactiveButtonItem, len(buttons))
	for i, b := range buttons {
		items[i] = interactiveButtonItem{Type: "reply", Reply: interactiveReply{ID: b.ID, Title: truncateTitle(b.Title)}}
	}

	reqBody, err := json.Marshal(interactiveButtonRequest{
		MessagingProduct: "whatsapp",
		To:               strings.TrimPrefix(to, "+"),
		Type:             "interactive",
		Interactive: interactiveButtonBody{
			Type:   "button",
			Body:   interactiveBodyText{Text: body},
			Action: interactiveButtonAction{Buttons: items},
		},
	})
	if err != nil {
		return "", err
	}
	return postMessage(ctx, accessToken, phoneNumberID, reqBody)
}

type interactiveListRequest struct {
	MessagingProduct string              `json:"messaging_product"`
	To               string              `json:"to"`
	Type             string              `json:"type"`
	Interactive      interactiveListBody `json:"interactive"`
}

type interactiveListBody struct {
	Type   string                `json:"type"`
	Body   interactiveBodyText   `json:"body"`
	Action interactiveListAction `json:"action"`
}

type interactiveListAction struct {
	Button   string                   `json:"button"`
	Sections []interactiveListSection `json:"sections"`
}

type interactiveListSection struct {
	Title string               `json:"title"`
	Rows  []interactiveListRow `json:"rows"`
}

type interactiveListRow struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// sendInteractiveList posts a list message (4-10 options, more than a
// button row can hold) via the Cloud API. Row titles are truncated the
// same way button titles are.
func sendInteractiveList(ctx context.Context, accessToken, phoneNumberID, to, body, buttonLabel string, sections []ai.ListSection) (string, error) {
	reqSections := make([]interactiveListSection, len(sections))
	for i, s := range sections {
		rows := make([]interactiveListRow, len(s.Options))
		for j, o := range s.Options {
			rows[j] = interactiveListRow{ID: o.ID, Title: truncateTitle(o.Title), Description: o.Description}
		}
		reqSections[i] = interactiveListSection{Title: s.Title, Rows: rows}
	}

	reqBody, err := json.Marshal(interactiveListRequest{
		MessagingProduct: "whatsapp",
		To:               strings.TrimPrefix(to, "+"),
		Type:             "interactive",
		Interactive: interactiveListBody{
			Type:   "list",
			Body:   interactiveBodyText{Text: body},
			Action: interactiveListAction{Button: buttonLabel, Sections: reqSections},
		},
	})
	if err != nil {
		return "", err
	}
	return postMessage(ctx, accessToken, phoneNumberID, reqBody)
}

type templateRequest struct {
	MessagingProduct string          `json:"messaging_product"`
	To               string          `json:"to"`
	Type             string          `json:"type"`
	Template         templateMessage `json:"template"`
}

type templateMessage struct {
	Name       string              `json:"name"`
	Language   templateLanguage    `json:"language"`
	Components []templateComponent `json:"components,omitempty"`
}

type templateLanguage struct {
	Code string `json:"code"`
}

type templateComponent struct {
	Type       string              `json:"type"`
	Parameters []templateParameter `json:"parameters"`
}

type templateParameter struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// sendTemplate posts a WhatsApp template message — the Cloud API's
// required shape for any business-initiated conversation outside an
// active 24-hour customer service window
// (docs/BRIEF-fixes-and-reminders.md #4). bodyParams fill the
// template's numbered {{1}}, {{2}}, ... placeholders in its one BODY
// component, in order — this client doesn't support header/button
// components, since the reminder template this exists for doesn't need
// them.
func sendTemplate(ctx context.Context, accessToken, phoneNumberID, to, templateName, languageCode string, bodyParams []string) (string, error) {
	params := make([]templateParameter, len(bodyParams))
	for i, p := range bodyParams {
		params[i] = templateParameter{Type: "text", Text: p}
	}

	reqBody, err := json.Marshal(templateRequest{
		MessagingProduct: "whatsapp",
		To:               strings.TrimPrefix(to, "+"),
		Type:             "template",
		Template: templateMessage{
			Name:     templateName,
			Language: templateLanguage{Code: languageCode},
			Components: []templateComponent{
				{Type: "body", Parameters: params},
			},
		},
	})
	if err != nil {
		return "", err
	}
	return postMessage(ctx, accessToken, phoneNumberID, reqBody)
}

// postMessage is the shared HTTP mechanics for every POST
// /{phone_number_id}/messages call (text, buttons, list) — only the
// marshaled body differs between them.
func postMessage(ctx context.Context, accessToken, phoneNumberID string, reqBody []byte) (string, error) {
	url := fmt.Sprintf("%s/%s/messages", graphAPIBaseURL, phoneNumberID)
	resp, err := doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+accessToken)
		return req, nil
	})
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

type markReadRequest struct {
	MessagingProduct string          `json:"messaging_product"`
	Status           string          `json:"status"`
	MessageID        string          `json:"message_id"`
	TypingIndicator  typingIndicator `json:"typing_indicator"`
}

type typingIndicator struct {
	Type string `json:"type"`
}

// markReadWithTyping posts the combined read-receipt + typing-indicator
// call (docs/BRIEF-polish-and-hardening.md #1) — Meta's own single-call
// shape, confirmed current: no separate "typing on" call exists. The
// indicator auto-dismisses after 25s or the real reply, whichever comes
// first, so there's nothing to re-trigger or clean up here.
func markReadWithTyping(ctx context.Context, accessToken, phoneNumberID, messageID string) error {
	reqBody, err := json.Marshal(markReadRequest{
		MessagingProduct: "whatsapp",
		Status:           "read",
		MessageID:        messageID,
		TypingIndicator:  typingIndicator{Type: "text"},
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/%s/messages", graphAPIBaseURL, phoneNumberID)
	resp, err := doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+accessToken)
		return req, nil
	})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, maxMediaResponseBytes)); err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("whatsapp: mark read failed with status %d", resp.StatusCode)
	}
	return nil
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
	resp, err := doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		return req, nil
	})
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

	dlResp, err := doWithRetry(ctx, func() (*http.Request, error) {
		dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, meta.URL, nil)
		if err != nil {
			return nil, err
		}
		dlReq.Header.Set("Authorization", "Bearer "+accessToken)
		return dlReq, nil
	})
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
