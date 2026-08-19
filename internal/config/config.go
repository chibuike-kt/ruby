// Package config loads process configuration from the environment,
// matching .env.example.
package config

import (
	"fmt"
	"os"
	"strconv"
)

const defaultRateLimitPerMinute = 30
const defaultAIModel = "gpt-5.6-terra"
const defaultTimezone = "Africa/Lagos"

type Config struct {
	Port               string
	Env                string
	DatabaseURL        string
	RedisURL           string
	RateLimitPerMinute int

	// WhatsAppAppSecret and WhatsAppVerifyToken are read as-is, with no
	// required-ness check: an empty value just means the webhook's
	// VerifySignature/VerifyHandshake checks fail closed (reject
	// everything) rather than the whole API refusing to start.
	WhatsAppAppSecret   string
	WhatsAppVerifyToken string

	// WhatsAppAccessToken and WhatsAppPhoneNumberID authenticate outbound
	// Cloud API calls (sending replies, downloading media). Same
	// fail-closed reasoning as above: empty just means those calls error
	// per-message rather than the process refusing to start.
	WhatsAppAccessToken   string
	WhatsAppPhoneNumberID string

	// AIProviderAPIKey empty means every AI call errors per-message
	// (surfaced to the trader as a friendly "something went wrong"),
	// not a boot failure — same reasoning as the WhatsApp secrets above.
	AIProviderAPIKey string
	AIModel          string

	// DefaultTimezone resolves relative due dates ("Friday") against a
	// concrete "today" before they ever reach the AI prompt (spec §21).
	DefaultTimezone string
}

func Load() (Config, error) {
	cfg := Config{
		Port:                  getEnv("PORT", "8080"),
		Env:                   getEnv("ENV", "development"),
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		RedisURL:              os.Getenv("REDIS_URL"),
		RateLimitPerMinute:    defaultRateLimitPerMinute,
		WhatsAppAppSecret:     os.Getenv("WHATSAPP_APP_SECRET"),
		WhatsAppVerifyToken:   os.Getenv("WHATSAPP_WEBHOOK_VERIFY_TOKEN"),
		WhatsAppAccessToken:   os.Getenv("WHATSAPP_ACCESS_TOKEN"),
		WhatsAppPhoneNumberID: os.Getenv("WHATSAPP_PHONE_NUMBER_ID"),
		AIProviderAPIKey:      os.Getenv("AI_PROVIDER_API_KEY"),
		AIModel:               getEnv("AI_MODEL", defaultAIModel),
		DefaultTimezone:       getEnv("DEFAULT_TIMEZONE", defaultTimezone),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("config: DATABASE_URL is required")
	}
	if cfg.RedisURL == "" {
		return Config{}, fmt.Errorf("config: REDIS_URL is required")
	}

	if v := os.Getenv("RATE_LIMIT_PER_MINUTE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("config: RATE_LIMIT_PER_MINUTE must be a positive integer, got %q", v)
		}
		cfg.RateLimitPerMinute = n
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
