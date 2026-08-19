// Package config loads process configuration from the environment,
// matching .env.example.
package config

import (
	"fmt"
	"os"
	"strconv"
)

const defaultRateLimitPerMinute = 30

type Config struct {
	Port               string
	Env                string
	DatabaseURL        string
	RedisURL           string
	RateLimitPerMinute int

	// WhatsAppAppSecret and WhatsAppVerifyToken are read as-is, with no
	// required-ness check: an empty value just means the webhook's
	// VerifySignature/VerifyHandshake checks fail closed (reject
	// everything) rather than the whole API refusing to start. The
	// other WhatsApp credentials (access token, phone number id, ...)
	// aren't read yet — they're for sending, a later slice.
	WhatsAppAppSecret   string
	WhatsAppVerifyToken string
}

func Load() (Config, error) {
	cfg := Config{
		Port:                getEnv("PORT", "8080"),
		Env:                 getEnv("ENV", "development"),
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		RedisURL:            os.Getenv("REDIS_URL"),
		RateLimitPerMinute:  defaultRateLimitPerMinute,
		WhatsAppAppSecret:   os.Getenv("WHATSAPP_APP_SECRET"),
		WhatsAppVerifyToken: os.Getenv("WHATSAPP_WEBHOOK_VERIFY_TOKEN"),
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
