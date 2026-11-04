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
	// Roles is what this process runs: "all", or a comma list of api, gateway and worker for a
	// split installation whose processes talk over NATS (NatsURL).
	Roles string
	Lang  string // notification language: en | id
	// InspectionModels are the vision models the dashboard offers for on-robot inspection.
	InspectionModels []string
	// ExtTokenTTL is how long an integration API access token lives.
	ExtTokenTTL time.Duration
	// LicenseFile is the signed license (NETRA-LIC-1...) to read at start; License is the same text
	// given directly. A license uploaded in the dashboard is the third source; the newest applies.
	LicenseFile string
	License     string

	// WebDir serves the dashboard's static export from a directory; empty: the one built into
	// the binary (build tag webui), or none.
	WebDir string

	// PGBin runs a private PostgreSQL from these binaries (initdb, pg_ctl, pg_dump) with its data
	// in DataDir/pgdata, instead of DatabaseURL; PGPort is its port on 127.0.0.1.
	PGBin  string
	PGPort int

	// Built-in final detection on Ollama, used when FinalDetectorURL is empty.
	OllamaURL      string
	AIModel        string
	AILanguage     string
	AISite         string // what the robot patrols, in the prompt: "mall", "substation"
	AICategories   []string
	AIMaxSide      int
	AITimeout      time.Duration
	AIKeepAlive    string
	AIConcurrency  int
	BackupKeepDays int    // daily database backups in DataDir/backups; 0 turns them off
	BackupAt       string // local time of the daily backup, HH:MM

	// TLS: a certificate and key, or "self-signed" in TLS to make one for TLSHosts.
	TLS      string
	TLSCert  string
	TLSKey   string
	TLSHosts []string
}

// Load reads the configuration, falling back to development defaults.
func Load() Config {
	tz, err := time.LoadLocation(env("NETRA_TZ", "Asia/Jakarta"))
	if err != nil {
		tz = time.UTC
	}
	ttlHours, _ := strconv.Atoi(env("NETRA_SESSION_HOURS", "72"))
	extMinutes, err := strconv.Atoi(env("NETRA_EXT_TOKEN_MINUTES", "60"))
	if err != nil || extMinutes < 5 || extMinutes > 24*60 {
		extMinutes = 60
	}
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
		Roles:            env("NETRA_ROLES", "all"),
		Lang:             env("NETRA_LANG", "en"),
		InspectionModels: list(env("NETRA_INSPECTION_MODELS",
			"qwen3-vl:2b-instruct,moondream:latest,ahmadwaqar/smolvlm2-2.2b-instruct:latest")),
		ExtTokenTTL:    time.Duration(extMinutes) * time.Minute,
		LicenseFile:    env("NETRA_LICENSE_FILE", ""),
		License:        env("NETRA_LICENSE", ""),
		WebDir:         env("NETRA_WEB_DIR", ""),
		PGBin:          env("NETRA_PG_BIN", ""),
		PGPort:         intEnv("NETRA_PG_PORT", 5433, 1, 65535),
		OllamaURL:      strings.TrimRight(env("NETRA_OLLAMA_URL", ""), "/"),
		AIModel:        env("NETRA_AI_MODEL", "qwen2.5vl:7b"),
		AILanguage:     env("NETRA_AI_LANGUAGE", "English"),
		AISite:         env("NETRA_AI_SITE", "mall"),
		AICategories:   list(env("NETRA_AI_CATEGORIES", "trash,spill,floor_damage,fallen_person")),
		AIMaxSide:      intEnv("NETRA_AI_MAX_SIDE", 1024, 128, 8192),
		AITimeout:      time.Duration(intEnv("NETRA_AI_TIMEOUT", 90, 5, 600)) * time.Second,
		AIKeepAlive:    env("NETRA_AI_KEEP_ALIVE", "1h"),
		AIConcurrency:  intEnv("NETRA_AI_CONCURRENCY", 1, 1, 16),
		BackupKeepDays: intEnv("NETRA_BACKUP_KEEP_DAYS", 14, 0, 3650),
		BackupAt:       env("NETRA_BACKUP_AT", "02:30"),
		TLS:            env("NETRA_TLS", ""),
		TLSCert:        env("NETRA_TLS_CERT", ""),
		TLSKey:         env("NETRA_TLS_KEY", ""),
		TLSHosts:       list(env("NETRA_TLS_HOSTS", "")),
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

// intEnv reads an integer within [lo, hi], else the fallback.
func intEnv(key string, fallback, lo, hi int) int {
	v, err := strconv.Atoi(env(key, ""))
	if err != nil || v < lo || v > hi {
		return fallback
	}
	return v
}

// LoadFile sets the KEY=VALUE lines of an environment file (comments and blank lines skipped,
// optional quotes removed) that are not already set, so the real environment wins. The installers
// keep the configuration in such a file (netra.env).
func LoadFile(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if _, set := os.LookupEnv(key); !set {
			_ = os.Setenv(key, value)
		}
	}
	return nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
