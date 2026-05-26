package config

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// SettingsGetter is implemented by *database.DB.
// Defined here to avoid an import cycle between config and database.
type SettingsGetter interface {
	GetSetting(ctx context.Context, key string) (string, bool)
}

type Config struct {
	// ── Bootstrap — loaded from .env, required before DB is open ─────────────
	APIID       int
	APIHash     string
	BotToken    string
	LogChannel  int64
	Port        int
	BindAddress string
	FQDN        string
	HasSSL      bool
	NoPort      bool
	Workers     int
	DBPath      string
	SessionDir  string
	OwnerID     int64
	Admins      []int64

	// ── DB-overrideable — defaults set in Load(), updated via ApplyDB ─────────
	MultiTokens          []string
	MaxStreamsPerToken    int
	RateLimitPerMin      int
	HashSecret           string
	MetricsEnabled       bool
	ShutdownTimeout      int
	AutoInsert           bool

	// XUI MySQL panel
	XUIHost     string
	XUIPort     int
	XUIUser     string
	XUIPassword string
	XUIDatabase string
	XUIServerID int
	XUIAdminID  int // ID do admin padrão na tabela admin (default 1)

	// TMDB API
	TMDBAPIKey   string
	TMDBLanguage string

	// XUI reload via SSH
	XUISSHHost           string
	XUISSHPort           int
	XUISSHUser           string
	XUISSHPassword       string
	XUIReloadCmd         string
	XUIReloadDebounceSec int

}

// Load reads the bootstrap settings from the environment / .env file.
// Only the fields needed to open the database and start the server are populated.
// Call cfg.ApplyDB after opening the database to load the rest.
func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		// Bootstrap
		APIHash:     getEnv("API_HASH", ""),
		BotToken:    getEnv("BOT_TOKEN", ""),
		BindAddress: getEnv("BIND_ADDRESS", "0.0.0.0"),
		FQDN:        getEnv("FQDN", getLocalIP()),
		HasSSL:      getEnvBool("HAS_SSL", false),
		NoPort:      getEnvBool("NO_PORT", false),
		DBPath:      getEnv("DB_PATH", "./syncgo.db"),
		SessionDir:  getEnv("SESSION_DIR", "./sessions"),

		// Defaults for DB-overrideable settings
		MaxStreamsPerToken:    8,
		RateLimitPerMin:      120,
		MetricsEnabled:       true,
		ShutdownTimeout:      30,
		XUIPort:              3306,
		XUIDatabase:          "xui",
		XUIServerID:          1,
		XUIAdminID:           1,
		XUISSHPort:           22,
		XUIReloadCmd:         "sudo service xuione reload",
		XUIReloadDebounceSec: 30,
		TMDBLanguage:         "pt-BR",
	}

	cfg.APIID = getEnvInt("API_ID", 0)
	cfg.LogChannel = getEnvInt64("LOG_CHANNEL", 0)
	cfg.Port = getEnvInt("PORT", 8080)
	cfg.Workers = getEnvInt("WORKERS", 4)
	cfg.OwnerID = getEnvInt64("OWNER_ID", 0)
	cfg.XUIAdminID = getEnvInt("XUI_ADMIN_ID", 1)
	if v := os.Getenv("XUI_HOST"); v != "" { cfg.XUIHost = v }
	if v := os.Getenv("XUI_USER"); v != "" { cfg.XUIUser = v }
	if v := os.Getenv("XUI_PASSWORD"); v != "" { cfg.XUIPassword = v }
	if v := os.Getenv("XUI_DATABASE"); v != "" { cfg.XUIDatabase = v }
	cfg.XUIPort = getEnvInt("XUI_PORT", cfg.XUIPort)
	cfg.XUIServerID = getEnvInt("XUI_SERVER_ID", cfg.XUIServerID)

	if admins := os.Getenv("ADMINS"); admins != "" {
		for _, a := range strings.Fields(strings.ReplaceAll(admins, ",", " ")) {
			if id, err := strconv.ParseInt(a, 10, 64); err == nil {
				cfg.Admins = append(cfg.Admins, id)
			}
		}
	}

	// Derive default hash secret; ApplyDB can override with a fixed value.
	cfg.HashSecret = "syncgo:" + cfg.BotToken

	if cfg.APIID == 0 {
		return nil, fmt.Errorf("API_ID is required")
	}
	if cfg.APIHash == "" {
		return nil, fmt.Errorf("API_HASH is required")
	}
	if cfg.BotToken == "" {
		return nil, fmt.Errorf("BOT_TOKEN is required")
	}
	if cfg.LogChannel == 0 {
		return nil, fmt.Errorf("LOG_CHANNEL is required")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("PORT must be between 1 and 65535, got %d", cfg.Port)
	}

	if err := os.MkdirAll(cfg.SessionDir, 0o755); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}

	return cfg, nil
}

// ApplyDB overlays database-stored settings on top of the defaults from Load.
// It is safe to call with a nil db (no-op).
// Call this after opening the database and before starting the Telegram pool.
func (c *Config) ApplyDB(ctx context.Context, db SettingsGetter) {
	if db == nil {
		return
	}
	get := func(key string) (string, bool) { return db.GetSetting(ctx, key) }
	str := func(key, def string) string {
		if v, ok := get(key); ok && v != "" {
			return v
		}
		return def
	}
	integer := func(key string, def int) int {
		if v, ok := get(key); ok && v != "" {
			if i, err := strconv.Atoi(v); err == nil {
				return i
			}
		}
		return def
	}
	boolean := func(key string, def bool) bool {
		v, ok := get(key)
		if !ok || v == "" {
			return def
		}
		switch strings.ToLower(v) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
		return def
	}

	// Extra bot tokens (stored as space/comma-separated list in settings table).
	if mt, ok := get("multi_tokens"); ok && mt != "" {
		seen := make(map[string]bool, len(c.MultiTokens))
		for _, t := range c.MultiTokens {
			seen[t] = true
		}
		for _, t := range strings.Fields(strings.ReplaceAll(mt, ",", " ")) {
			if t = strings.TrimSpace(t); t != "" && !seen[t] {
				c.MultiTokens = append(c.MultiTokens, t)
				seen[t] = true
			}
		}
	}

	c.MaxStreamsPerToken = integer("max_streams_per_token", c.MaxStreamsPerToken)
	c.RateLimitPerMin = integer("rate_limit_per_min", c.RateLimitPerMin)
	c.MetricsEnabled = boolean("metrics_enabled", c.MetricsEnabled)
	c.ShutdownTimeout = integer("shutdown_timeout", c.ShutdownTimeout)
	if hs, ok := get("hash_secret"); ok && hs != "" {
		c.HashSecret = hs
	}

	c.XUIHost = str("xui_host", c.XUIHost)
	c.XUIPort = integer("xui_port", c.XUIPort)
	c.XUIUser = str("xui_user", c.XUIUser)
	c.XUIPassword = str("xui_password", c.XUIPassword)
	c.XUIDatabase = str("xui_database", c.XUIDatabase)
	c.XUIServerID = integer("xui_server_id", c.XUIServerID)
	c.XUIAdminID = integer("xui_admin_id", c.XUIAdminID)

	// SSH host defaults to xui_host when not set separately.
	c.XUISSHHost = str("xui_ssh_host", c.XUIHost)
	c.XUISSHPort = integer("xui_ssh_port", c.XUISSHPort)
	c.XUISSHUser = str("xui_ssh_user", c.XUISSHUser)
	c.XUISSHPassword = str("xui_ssh_password", c.XUISSHPassword)
	c.XUIReloadCmd = str("xui_reload_cmd", c.XUIReloadCmd)
	c.XUIReloadDebounceSec = integer("xui_reload_debounce_sec", c.XUIReloadDebounceSec)

	c.TMDBAPIKey = str("tmdb_api_key", c.TMDBAPIKey)
	c.TMDBLanguage = str("tmdb_language", c.TMDBLanguage)

	// AutoInsert is enabled automatically when XUI is configured.
	c.AutoInsert = boolean("auto_insert", c.XUIHost != "")
}

func (c *Config) XUIEnabled() bool {
	return c.XUIHost != "" && c.XUIUser != "" && c.XUIDatabase != ""
}

func (c *Config) XUIReloadEnabled() bool {
	return c.XUISSHHost != "" && c.XUISSHUser != "" && c.XUISSHPassword != ""
}

func (c *Config) URLBase() string {
	scheme := "http"
	if c.HasSSL {
		scheme = "https"
	}
	if c.NoPort {
		return fmt.Sprintf("%s://%s", scheme, c.FQDN)
	}
	return fmt.Sprintf("%s://%s:%d", scheme, c.FQDN, c.Port)
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func getEnvInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i
		}
	}
	return def
}

func getLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "localhost"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

func getEnvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "y", "on":
			return true
		case "0", "false", "no", "n", "off":
			return false
		}
	}
	return def
}
