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
