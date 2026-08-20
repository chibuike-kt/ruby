package whatsapp

// SetGraphAPIBaseURLForTest points graphAPIBaseURL at a test server —
// the standard export_test.go seam so whatsapp_test (an external test
// package) can stub the Cloud API without a production-facing setter.
// Every ReceiveEvent test needs this now that receiveMessage also fires
// a mark-read/typing-indicator call (docs/BRIEF-polish-and-hardening.md
// #1) before handing off to the AI pipeline.
func SetGraphAPIBaseURLForTest(url string) func() {
	original := graphAPIBaseURL
	graphAPIBaseURL = url
	return func() { graphAPIBaseURL = original }
}
