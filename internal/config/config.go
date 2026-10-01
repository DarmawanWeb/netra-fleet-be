// Package config reads the service configuration from the environment.
package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds every runtime setting of netra-api.
type Config struct {
	Addr             string
	DatabaseURL      string
	DataDir          string
	TimeZone         *time.Location
	AdminUser        string
	AdminPassword    string
	TelegramToken    string
	TelegramChatID   string
	TelegramAPI      string
	PublicURL        string
	FinalDetectorURL string
	SessionTTL       time.Duration
	AllowedOrigin    string
	NatsURL          string
	Lang             string // notification language: en | id
}

// Load reads the configuration, falling back to development defaults.
func Load() Config {
	tz, err := time.LoadLocation(env("NETRA_TZ", "Asia/Jakarta"))
	if err != nil {
		tz = time.UTC
	}
	ttlHours, _ := strconv.Atoi(env("NETRA_SESSION_HOURS", "72"))
	return Config{
		Addr:             env("NETRA_ADDR", ":8080"),
		DatabaseURL:      env("NETRA_DATABASE_URL", "postgres://netra:netra@localhost:5432/netra?sslmode=disable"),
		DataDir:          env("NETRA_DATA_DIR", "./data"),
		TimeZone:         tz,
		AdminUser:        env("NETRA_ADMIN_USER", "admin"),
		AdminPassword:    env("NETRA_ADMIN_PASSWORD", ""),
		TelegramToken:    env("NETRA_TELEGRAM_TOKEN", ""),
		TelegramChatID:   env("NETRA_TELEGRAM_CHAT_ID", ""),
		TelegramAPI:      env("NETRA_TELEGRAM_API", "https://api.telegram.org"),
		PublicURL:        env("NETRA_PUBLIC_URL", ""),
		FinalDetectorURL: env("NETRA_FINAL_DETECTOR_URL", ""),
		SessionTTL:       time.Duration(ttlHours) * time.Hour,
		AllowedOrigin:    env("NETRA_ALLOWED_ORIGIN", "*"),
		NatsURL:          env("NETRA_NATS_URL", ""),
		Lang:             env("NETRA_LANG", "en"),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
