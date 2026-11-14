package config

import (
	"os"
	"testing"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"DATABASE_URL", "WAYPOINT_ENV", "WAYPOINT_HTTP_ADDR", "WAYPOINT_SESSION_TTL_HOURS",
		"WAYPOINT_COOKIE_SECURE", "ADMIN_BOOTSTRAP_TOKEN", "WAYPOINT_CORS_ORIGIN",
	} {
		os.Unsetenv(k)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	clearEnv(t)
	if _, err := Load(); err == nil {
		t.Fatal("expected an error when DATABASE_URL is unset")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.CookieSecure {
		t.Error("CookieSecure should default to false for local development")
	}
	if cfg.SessionTTL.Hours() != 168 {
		t.Errorf("SessionTTL = %v, want 168h", cfg.SessionTTL)
	}
	if cfg.AdminBootstrapToken != "" {
		t.Error("AdminBootstrapToken should default to empty (bootstrap disabled)")
	}
	if cfg.MCPTokenTTL.Hours() != 180*24 {
		t.Errorf("MCPTokenTTL = %v, want 180 days", cfg.MCPTokenTTL)
	}
	if cfg.CITokenTTL.Hours() != 90*24 {
		t.Errorf("CITokenTTL = %v, want 90 days", cfg.CITokenTTL)
	}
	if cfg.AuthRateLimitPerMin != 10 {
		t.Errorf("AuthRateLimitPerMin = %d, want 10", cfg.AuthRateLimitPerMin)
	}
}
