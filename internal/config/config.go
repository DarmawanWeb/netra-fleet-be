// Package config reads the service configuration from the environment.
package config

import (
	"os"
	"strconv"
	"strings"
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
	// InspectionModels are the vision models the dashboard offers for on-robot inspection.
	InspectionModels []string
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
		InspectionModels: list(env("NETRA_INSPECTION_MODELS",
			"qwen3-vl:2b-instruct,moondream:latest,ahmadwaqar/smolvlm2-2.2b-instruct:latest")),
	}
}

// list splits a comma-separated value, dropping blanks.
func list(v string) []string {
	out := []string{}
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
