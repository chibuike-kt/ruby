package ai

// Reply is Processor's answer to an inbound message: the text body that
// always accompanies it (WhatsApp interactive messages require body
// text regardless, and it's what a client without interactive support
// falls back to), plus optional interactive presentation. Buttons/List
// are an enhancement, never a replacement — Text alone is always a
// complete, correct answer on its own
// (docs/BRIEF-interactive-messages.md: "Keep the existing text-reply
// matching path working too").
type Reply struct {
	Text    string
	Buttons []Button
	List    *ListPayload
}

func textReply(s string) Reply { return Reply{Text: s} }

// Button is one WhatsApp reply button (max 3 per message). ID is what
// comes back in the inbound button_reply webhook event — for
// disambiguation this is the candidate's customer_id (as a decimal
// string, per docs/BRIEF-interactive-messages.md — "no need to re-run
// the deterministic text matching... since the id is the answer"), for
// confirmation it's "confirm"/"edit"/"cancel", for the greeting menu a
// "menu:..." id. Title is truncated to a safe on-screen length by the
// transport layer (internal/whatsapp) before sending — ai only decides
// what the button means, not how it fits on a small screen.
type Button struct {
	ID    string
	Title string
}

// ListPayload is a WhatsApp list message (4-10 options, more than a
// button row can hold), grouped into sections. ButtonLabel is the text
// on the button that opens the list (WhatsApp's own "action.button").
type ListPayload struct {
	ButtonLabel string
	Sections    []ListSection
}

type ListSection struct {
	Title   string
	Options []ListOption
}

type ListOption struct {
	ID          string
	Title       string
	Description string
}
