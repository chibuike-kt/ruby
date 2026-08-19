package whatsapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const signatureHeaderPrefix = "sha256="

// VerifySignature checks Meta's X-Hub-Signature-256 header: an
// HMAC-SHA256 of the raw request body, keyed with the app secret, hex
// encoded and prefixed "sha256=" (spec §35 — this is the actual
// security boundary for the webhook, not the verify token, which only
// covers the one-time GET handshake). An empty appSecret always fails
// closed rather than accepting an effectively-unsigned request.
func VerifySignature(appSecret string, body []byte, header string) bool {
	if appSecret == "" {
		return false
	}
	hexDigest, ok := strings.CutPrefix(header, signatureHeaderPrefix)
	if !ok {
		return false
	}
	expected, err := hex.DecodeString(hexDigest)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	computed := mac.Sum(nil)

	return hmac.Equal(computed, expected)
}
