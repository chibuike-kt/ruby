// Package whatsapp receives, verifies, dedupes, and stores inbound
// WhatsApp Cloud API webhook events (spec §3, §15/§31). This slice
// stops at "message safely stored, ready for processing" — no AI
// intent extraction, no response generation, no voice transcription;
// those are later slices.
package whatsapp

// Payload shapes match Meta's WhatsApp Cloud API webhook payload
// examples (developers.facebook.com/docs/whatsapp/cloud-api/webhooks/
// payload-examples), verified directly rather than assumed — the brief
// for this slice flagged payload-shape drift as an expensive class of
// bug to get wrong blind.

type WebhookPayload struct {
	Object string  `json:"object"`
	Entry  []Entry `json:"entry"`
}

type Entry struct {
	ID      string   `json:"id"`
	Changes []Change `json:"changes"`
}

type Change struct {
	Value Value  `json:"value"`
	Field string `json:"field"`
}

// Value's Messages is present for inbound message events; a delivery
// receipt (sent/delivered/read) instead populates a Statuses field we
// don't model here — this slice only cares about inbound messages, and
// an unrecognized value shape just yields zero messages to process
// rather than an error.
type Value struct {
	MessagingProduct string    `json:"messaging_product"`
	Metadata         Metadata  `json:"metadata"`
	Contacts         []Contact `json:"contacts"`
	Messages         []Message `json:"messages"`
}

type Metadata struct {
	DisplayPhoneNumber string `json:"display_phone_number"`
	PhoneNumberID      string `json:"phone_number_id"`
}

type Contact struct {
	Profile Profile `json:"profile"`
	WaID    string  `json:"wa_id"`
}

type Profile struct {
	Name string `json:"name"`
}

// Message is one entry in value.messages[]. Text, Audio, Image, and
// Interactive are populated depending on Type — other message types
// (document, location, ...) are accepted and stored with an empty
// content reference rather than rejected, per spec §37 (malformed/
// unexpected input must not crash the handler). Image is docs/BRIEF-
// research-hardening-standard.md Part 5 Tier 1's photo input — same
// shape as Audio, since both are just a WhatsApp media id to download.
type Message struct {
	From        string       `json:"from"`
	ID          string       `json:"id"`
	Timestamp   string       `json:"timestamp"`
	Type        string       `json:"type"`
	Text        *Text        `json:"text,omitempty"`
	Audio       *Media       `json:"audio,omitempty"`
	Image       *Media       `json:"image,omitempty"`
	Interactive *Interactive `json:"interactive,omitempty"`
}

type Text struct {
	Body string `json:"body"`
}

// Interactive is populated when Type == "interactive": a trader tapped
// a reply button or a list option Ruby sent earlier
// (docs/BRIEF-interactive-messages.md). Exactly one of ButtonReply/
// ListReply is set, matching Interactive.Type ("button_reply" or
// "list_reply") — both carry the id Ruby chose when building the
// button/list, never free text.
type Interactive struct {
	Type        string            `json:"type"`
	ButtonReply *InteractiveReply `json:"button_reply,omitempty"`
	ListReply   *InteractiveReply `json:"list_reply,omitempty"`
}

type InteractiveReply struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Media covers audio (and would cover other media types, if this slice
// stored them) — only the id is used here. Actual media retrieval is a
// separate, later slice; this stores the WhatsApp media id as-is.
type Media struct {
	ID       string `json:"id"`
	MimeType string `json:"mime_type"`
}
