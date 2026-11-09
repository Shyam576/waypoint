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
