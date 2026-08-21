package config_test

import (
	"testing"

	"github.com/chibuike-kt/ruby/internal/config"
)

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://ruby:ruby@localhost:5432/ruby")
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("PORT", "")
	t.Setenv("RATE_LIMIT_PER_MINUTE", "")
	t.Setenv("AI_MODEL", "")
	t.Setenv("DEFAULT_TIMEZONE", "")
	t.Setenv("WHATSAPP_CUSTOMER_REMINDER_TEMPLATE_NAME", "")
	t.Setenv("WHATSAPP_TRADER_REMINDER_TEMPLATE_NAME", "")
	t.Setenv("WHATSAPP_WEEKLY_DIGEST_TEMPLATE_NAME", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != "8080" {
		t.Fatalf("got port %q, want 8080", cfg.Port)
	}
	if cfg.RateLimitPerMinute != 30 {
		t.Fatalf("got rate limit %d, want 30", cfg.RateLimitPerMinute)
	}
	if cfg.AIModel != "gpt-5.6-terra" {
		t.Fatalf("got AI model %q, want gpt-5.6-terra", cfg.AIModel)
	}
	if cfg.DefaultTimezone != "Africa/Lagos" {
		t.Fatalf("got default timezone %q, want Africa/Lagos", cfg.DefaultTimezone)
	}
	if cfg.CustomerReminderTemplateName != "debt_reminder_customer" {
		t.Fatalf("got customer reminder template name %q, want debt_reminder_customer", cfg.CustomerReminderTemplateName)
	}
	if cfg.TraderReminderTemplateName != "debt_reminder_trader" {
		t.Fatalf("got trader reminder template name %q, want debt_reminder_trader", cfg.TraderReminderTemplateName)
	}
	if cfg.WeeklyDigestTemplateName != "weekly_digest" {
		t.Fatalf("got weekly digest template name %q, want weekly_digest", cfg.WeeklyDigestTemplateName)
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")

	if _, err := config.Load(); err == nil {
		t.Fatal("expected error for missing DATABASE_URL")
	}
}

func TestLoad_InvalidRateLimit(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://ruby:ruby@localhost:5432/ruby")
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("RATE_LIMIT_PER_MINUTE", "not-a-number")

	if _, err := config.Load(); err == nil {
		t.Fatal("expected error for invalid RATE_LIMIT_PER_MINUTE")
	}
}
