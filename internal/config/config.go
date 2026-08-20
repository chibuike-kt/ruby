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

// defaultCustomerReminderTemplateName and defaultTraderReminderTemplateName
// are placeholders until real templates are submitted and approved by
// Meta (docs/BRIEF-fixes-and-reminders.md #4, docs/BRIEF-critical-
// fixes-and-reminders.md's full reminder system) — two separate
// templates, since the customer-facing and trader-facing message
// bodies are genuinely different (spec §18's own two examples).
// Configurable via env so swapping in the approved names is a config
// change, not a code change.
const defaultCustomerReminderTemplateName = "debt_reminder_customer"
const defaultTraderReminderTemplateName = "debt_reminder_trader"

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

	// CustomerReminderTemplateName and TraderReminderTemplateName are
	// the two Meta-approved WhatsApp templates reminders are sent
	// through (docs/BRIEF-fixes-and-reminders.md #4, docs/BRIEF-
	// critical-fixes-and-reminders.md's full reminder system). Until
	// real ones are approved, Dispatch will fail every send with a
	// real Cloud API error naming these templates — see
	// internal/reminder.Service.Dispatch's doc comment.
	CustomerReminderTemplateName string
	TraderReminderTemplateName   string

	// WemaPartnerToken authenticates the Credit Profile API
	// (docs/wema-integration.md) — a static bearer token for the demo,
	// stubbing what would be a proper OAuth2 client-credentials flow
	// issued to Wema specifically in production. Empty means the
	// endpoint fails closed (rejects everything), same reasoning as
	// WhatsAppAppSecret/WhatsAppVerifyToken above.
	WemaPartnerToken string
}

func Load() (Config, error) {
	cfg := Config{
		Port:                         getEnv("PORT", "8080"),
		Env:                          getEnv("ENV", "development"),
		DatabaseURL:                  os.Getenv("DATABASE_URL"),
		RedisURL:                     os.Getenv("REDIS_URL"),
		RateLimitPerMinute:           defaultRateLimitPerMinute,
		WhatsAppAppSecret:            os.Getenv("WHATSAPP_APP_SECRET"),
		WhatsAppVerifyToken:          os.Getenv("WHATSAPP_WEBHOOK_VERIFY_TOKEN"),
		WhatsAppAccessToken:          os.Getenv("WHATSAPP_ACCESS_TOKEN"),
		WhatsAppPhoneNumberID:        os.Getenv("WHATSAPP_PHONE_NUMBER_ID"),
		AIProviderAPIKey:             os.Getenv("AI_PROVIDER_API_KEY"),
		AIModel:                      getEnv("AI_MODEL", defaultAIModel),
		DefaultTimezone:              getEnv("DEFAULT_TIMEZONE", defaultTimezone),
		CustomerReminderTemplateName: getEnv("WHATSAPP_CUSTOMER_REMINDER_TEMPLATE_NAME", defaultCustomerReminderTemplateName),
		TraderReminderTemplateName:   getEnv("WHATSAPP_TRADER_REMINDER_TEMPLATE_NAME", defaultTraderReminderTemplateName),
		WemaPartnerToken:             os.Getenv("WEMA_PARTNER_TOKEN"),
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
