// Package config loads the service configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"time"
)

// Defaults applied when the matching environment variable is unset or empty.
const (
	DefaultListenAddr      = ":8080"
	DefaultSlotGranularity = 15 * time.Minute
)

// Config is the runtime configuration of the service.
type Config struct {
	// APIKey authenticates callers of the /v1 routes (X-API-Key header).
	APIKey string
	// ListenAddr is the TCP address the HTTP server binds to.
	ListenAddr string
	// DatabaseURL is the PostgreSQL connection string for pgxpool.
	DatabaseURL string
	// SlotGranularity is the start-time grid step (assumption A7).
	SlotGranularity time.Duration
	// OTLPEndpoint is the OTLP collector endpoint. Empty disables trace export.
	OTLPEndpoint string
}

// Load reads the configuration through getenv, which is os.Getenv in
// production and a map lookup in tests.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		APIKey:          getenv("API_KEY"),
		ListenAddr:      getenv("LISTEN_ADDR"),
		DatabaseURL:     getenv("DATABASE_URL"),
		SlotGranularity: DefaultSlotGranularity,
		OTLPEndpoint:    getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = DefaultListenAddr
	}

	var errs []error
	if cfg.APIKey == "" {
		errs = append(errs, errors.New("API_KEY is required"))
	}
	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if raw := getenv("SLOT_GRANULARITY"); raw != "" {
		d, err := time.ParseDuration(raw)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("SLOT_GRANULARITY: %w", err))
		case d < time.Minute || d%time.Minute != 0:
			errs = append(errs, fmt.Errorf("SLOT_GRANULARITY must be a positive whole number of minutes, got %s", raw))
		case (24*time.Hour)%d != 0:
			errs = append(errs, fmt.Errorf("SLOT_GRANULARITY must divide a day evenly, got %s", raw))
		default:
			cfg.SlotGranularity = d
		}
	}

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return cfg, nil
}
