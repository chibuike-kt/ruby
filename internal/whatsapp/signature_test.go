package whatsapp_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/chibuike-kt/ruby/internal/whatsapp"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature_Valid(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account"}`)
	header := sign("s3cr3t", body)

	if !whatsapp.VerifySignature("s3cr3t", body, header) {
		t.Fatal("got false for a correctly computed signature, want true")
	}
}

func TestVerifySignature_WrongSecret(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account"}`)
	header := sign("s3cr3t", body)

	if whatsapp.VerifySignature("different-secret", body, header) {
		t.Fatal("got true for a signature computed with a different secret, want false")
	}
}

func TestVerifySignature_TamperedBody(t *testing.T) {
	original := []byte(`{"object":"whatsapp_business_account"}`)
	header := sign("s3cr3t", original)
	tampered := []byte(`{"object":"tampered"}`)

	if whatsapp.VerifySignature("s3cr3t", tampered, header) {
		t.Fatal("got true for a body that doesn't match the signature, want false")
	}
}

func TestVerifySignature_MissingHeader(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account"}`)

	if whatsapp.VerifySignature("s3cr3t", body, "") {
		t.Fatal("got true for an empty signature header, want false")
	}
}

func TestVerifySignature_MissingPrefix(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account"}`)
	mac := hmac.New(sha256.New, []byte("s3cr3t"))
	mac.Write(body)
	rawHex := hex.EncodeToString(mac.Sum(nil))

	if whatsapp.VerifySignature("s3cr3t", body, rawHex) {
		t.Fatal("got true for a header missing the sha256= prefix, want false")
	}
}

func TestVerifySignature_NonHexHeader(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account"}`)

	if whatsapp.VerifySignature("s3cr3t", body, "sha256=not-hex-at-all") {
		t.Fatal("got true for a non-hex signature, want false")
	}
}

// An unconfigured app secret must fail closed — accepting anything
// when the secret is blank would be equivalent to no signature
// verification at all, exactly what spec §35 forbids.
func TestVerifySignature_EmptySecretAlwaysFails(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account"}`)
	header := sign("", body)

	if whatsapp.VerifySignature("", body, header) {
		t.Fatal("got true with an empty app secret, want false (fail closed)")
	}
}
