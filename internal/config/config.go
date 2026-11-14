// Package config loads Waypoint's runtime configuration from environment
// variables. All values are read once at startup and validated so the
// process fails fast instead of surfacing confusing errors later.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Env                 string
	HTTPAddr            string
	DatabaseURL         string
	SessionCookieName   string
	SessionTTL          time.Duration
	CookieSecure        bool
	AdminBootstrapToken string
	DeployAuthTTL       time.Duration
	MCPTokenTTL         time.Duration
	CITokenTTL          time.Duration
	AuthRateLimitPerMin int
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	IdleTimeout         time.Duration
	ShutdownTimeout     time.Duration
	CORSOrigin          string
}

func Load() (Config, error) {
	cfg := Config{
		Env:                 getEnv("WAYPOINT_ENV", "development"),
		HTTPAddr:            getEnv("WAYPOINT_HTTP_ADDR", ":8080"),
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		SessionCookieName:   getEnv("WAYPOINT_SESSION_COOKIE", "waypoint_session"),
		AdminBootstrapToken: os.Getenv("ADMIN_BOOTSTRAP_TOKEN"),
		CORSOrigin:          getEnv("WAYPOINT_CORS_ORIGIN", "http://localhost:3000"),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}

	ttlHours, err := strconv.Atoi(getEnv("WAYPOINT_SESSION_TTL_HOURS", "168"))
	if err != nil {
		return Config{}, fmt.Errorf("WAYPOINT_SESSION_TTL_HOURS must be an integer: %w", err)
	}
	cfg.SessionTTL = time.Duration(ttlHours) * time.Hour

	cfg.CookieSecure = getEnv("WAYPOINT_COOKIE_SECURE", "false") == "true"

	authTTLSeconds, err := strconv.Atoi(getEnv("WAYPOINT_DEPLOY_AUTH_TTL_SECONDS", "900"))
	if err != nil {
		return Config{}, fmt.Errorf("WAYPOINT_DEPLOY_AUTH_TTL_SECONDS must be an integer: %w", err)
	}
	if authTTLSeconds <= 0 || authTTLSeconds > 3600 {
		return Config{}, fmt.Errorf("WAYPOINT_DEPLOY_AUTH_TTL_SECONDS must be between 1 and 3600")
	}
	cfg.DeployAuthTTL = time.Duration(authTTLSeconds) * time.Second

	mcpTTLDays, err := strconv.Atoi(getEnv("WAYPOINT_MCP_TOKEN_TTL_DAYS", "180"))
	if err != nil || mcpTTLDays <= 0 {
		return Config{}, fmt.Errorf("WAYPOINT_MCP_TOKEN_TTL_DAYS must be a positive integer")
	}
	cfg.MCPTokenTTL = time.Duration(mcpTTLDays) * 24 * time.Hour

	ciTTLDays, err := strconv.Atoi(getEnv("WAYPOINT_CI_TOKEN_TTL_DAYS", "90"))
	if err != nil || ciTTLDays <= 0 {
		return Config{}, fmt.Errorf("WAYPOINT_CI_TOKEN_TTL_DAYS must be a positive integer")
	}
	cfg.CITokenTTL = time.Duration(ciTTLDays) * 24 * time.Hour

	rateLimit, err := strconv.Atoi(getEnv("WAYPOINT_AUTH_RATE_LIMIT_PER_MINUTE", "10"))
	if err != nil || rateLimit <= 0 {
		return Config{}, fmt.Errorf("WAYPOINT_AUTH_RATE_LIMIT_PER_MINUTE must be a positive integer")
	}
	cfg.AuthRateLimitPerMin = rateLimit

	cfg.ReadTimeout = 10 * time.Second
	cfg.WriteTimeout = 15 * time.Second
	cfg.IdleTimeout = 60 * time.Second
	cfg.ShutdownTimeout = 10 * time.Second

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
