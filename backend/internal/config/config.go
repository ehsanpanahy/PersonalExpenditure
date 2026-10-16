package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

type Config struct {
	Address                string
	SupabaseURL            string
	SupabasePublishableKey string
	DatabaseURL            string
	UpstreamTimeout        time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		Address:                valueOrDefault("API_ADDR", ":8080"),
		SupabaseURL:            strings.TrimRight(strings.TrimSpace(os.Getenv("SUPABASE_URL")), "/"),
		SupabasePublishableKey: strings.TrimSpace(os.Getenv("SUPABASE_PUBLISHABLE_KEY")),
		DatabaseURL:            strings.TrimSpace(os.Getenv("DATABASE_URL")),
		UpstreamTimeout:        8 * time.Second,
	}

	if cfg.SupabaseURL == "" {
		return Config{}, errors.New("SUPABASE_URL is required")
	}
	parsedURL, err := url.ParseRequestURI(cfg.SupabaseURL)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return Config{}, errors.New("SUPABASE_URL must be an absolute HTTP or HTTPS URL")
	}
	if cfg.SupabasePublishableKey == "" {
		return Config{}, errors.New("SUPABASE_PUBLISHABLE_KEY is required")
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	databaseURL, err := url.Parse(cfg.DatabaseURL)
	if err != nil || (databaseURL.Scheme != "postgres" && databaseURL.Scheme != "postgresql") || databaseURL.Host == "" {
		return Config{}, errors.New("DATABASE_URL must be an absolute postgres:// or postgresql:// URL")
	}
	if databaseURL.User == nil || databaseURL.User.Username() != "app_api_login" {
		return Config{}, errors.New("DATABASE_URL must use the restricted app_api_login role")
	}
	host := strings.ToLower(databaseURL.Hostname())
	sslMode := databaseURL.Query().Get("sslmode")
	if host != "localhost" && host != "127.0.0.1" && host != "::1" &&
		sslMode != "require" && sslMode != "verify-ca" && sslMode != "verify-full" {
		return Config{}, errors.New("DATABASE_URL must require TLS for non-local PostgreSQL hosts")
	}
	if cfg.Address == "" {
		return Config{}, fmt.Errorf("API_ADDR must not be empty")
	}

	return cfg, nil
}

func valueOrDefault(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}
