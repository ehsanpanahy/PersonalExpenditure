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
	UpstreamTimeout        time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		Address:                valueOrDefault("API_ADDR", ":8080"),
		SupabaseURL:            strings.TrimRight(strings.TrimSpace(os.Getenv("SUPABASE_URL")), "/"),
		SupabasePublishableKey: strings.TrimSpace(os.Getenv("SUPABASE_PUBLISHABLE_KEY")),
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
