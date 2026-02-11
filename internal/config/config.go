package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultSyncTimeoutMS    = 30000
	defaultSearchCommand    = "/search"
	defaultMaxResults       = 5
	defaultReplyMode        = "thread"
	defaultMaxQueryLen      = 200
	defaultAddPath          = "/add"
	defaultSearchWSPath     = "/search"
	defaultRequestTimeoutMS = 10000
	defaultStateDBPath      = "/var/lib/matrix-bot/state.db"
	defaultCryptoDBPath     = "/var/lib/matrix-bot/crypto.db"
)

// Config is the root runtime configuration loaded from YAML.
type Config struct {
	Matrix  MatrixConfig  `yaml:"matrix"`
	Bot     BotConfig     `yaml:"bot"`
	Hister  HisterConfig  `yaml:"hister"`
	HTTP    HTTPConfig    `yaml:"http"`
	Storage StorageConfig `yaml:"storage"`
}

type MatrixConfig struct {
	HomeserverURL  string   `yaml:"homeserver_url"`
	UserID         string   `yaml:"user_id"`
	AccessToken    string   `yaml:"access_token"`
	DeviceID       string   `yaml:"device_id"`
	BotDisplayName string   `yaml:"bot_display_name"`
	SyncTimeoutMS  int      `yaml:"sync_timeout_ms"`
	AllowedRoomIDs []string `yaml:"allowed_room_ids"`
}

type BotConfig struct {
	SearchCommand string `yaml:"search_command"`
	MaxResults    int    `yaml:"max_results"`
	ReplyMode     string `yaml:"reply_mode"`
	MaxQueryLen   int    `yaml:"max_query_len"`
}

type HisterConfig struct {
	BaseURL      string `yaml:"base_url"`
	AddPath      string `yaml:"add_path"`
	SearchWSPath string `yaml:"search_ws_path"`
}

type HTTPConfig struct {
	RequestTimeoutMS int `yaml:"request_timeout_ms"`
}

type StorageConfig struct {
	StateDBPath  string `yaml:"state_db_path"`
	CryptoDBPath string `yaml:"crypto_db_path"`
}

func DefaultConfig() Config {
	return Config{
		Matrix: MatrixConfig{
			SyncTimeoutMS: defaultSyncTimeoutMS,
		},
		Bot: BotConfig{
			SearchCommand: defaultSearchCommand,
			MaxResults:    defaultMaxResults,
			ReplyMode:     defaultReplyMode,
			MaxQueryLen:   defaultMaxQueryLen,
		},
		Hister: HisterConfig{
			AddPath:      defaultAddPath,
			SearchWSPath: defaultSearchWSPath,
		},
		HTTP: HTTPConfig{
			RequestTimeoutMS: defaultRequestTimeoutMS,
		},
		Storage: StorageConfig{
			StateDBPath:  defaultStateDBPath,
			CryptoDBPath: defaultCryptoDBPath,
		},
	}
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg, err := Parse(raw)
	if err != nil {
		return nil, err
	}

	base := filepath.Dir(path)
	cfg.Storage.StateDBPath = resolvePath(base, cfg.Storage.StateDBPath)
	cfg.Storage.CryptoDBPath = resolvePath(base, cfg.Storage.CryptoDBPath)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func Parse(raw []byte) (*Config, error) {
	cfg := DefaultConfig()
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse config yaml: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	if c == nil {
		return errors.New("config is nil")
	}

	var validationErrs []string

	if err := validateHTTPURL(c.Matrix.HomeserverURL); err != nil {
		validationErrs = append(validationErrs, fmt.Sprintf("matrix.homeserver_url: %v", err))
	}
	if strings.TrimSpace(c.Matrix.UserID) == "" {
		validationErrs = append(validationErrs, "matrix.user_id is required")
	}
	if strings.TrimSpace(c.Matrix.AccessToken) == "" {
		validationErrs = append(validationErrs, "matrix.access_token is required")
	}
	if strings.TrimSpace(c.Matrix.BotDisplayName) == "" {
		validationErrs = append(validationErrs, "matrix.bot_display_name is required")
	}
	if c.Matrix.SyncTimeoutMS <= 0 {
		validationErrs = append(validationErrs, "matrix.sync_timeout_ms must be > 0")
	}
	if len(c.Matrix.AllowedRoomIDs) == 0 {
		validationErrs = append(validationErrs, "matrix.allowed_room_ids must include at least one room")
	}
	for i, roomID := range c.Matrix.AllowedRoomIDs {
		roomID = strings.TrimSpace(roomID)
		if roomID == "" {
			validationErrs = append(validationErrs, fmt.Sprintf("matrix.allowed_room_ids[%d] is empty", i))
			continue
		}
		if !strings.HasPrefix(roomID, "!") {
			validationErrs = append(validationErrs, fmt.Sprintf("matrix.allowed_room_ids[%d] must start with '!'", i))
		}
	}

	if strings.TrimSpace(c.Bot.SearchCommand) == "" {
		validationErrs = append(validationErrs, "bot.search_command is required")
	}
	if c.Bot.MaxResults <= 0 {
		validationErrs = append(validationErrs, "bot.max_results must be > 0")
	}
	if strings.TrimSpace(c.Bot.ReplyMode) == "" {
		validationErrs = append(validationErrs, "bot.reply_mode is required")
	} else if c.Bot.ReplyMode != "thread" {
		validationErrs = append(validationErrs, "bot.reply_mode must be 'thread' for this PoC")
	}
	if c.Bot.MaxQueryLen <= 0 {
		validationErrs = append(validationErrs, "bot.max_query_len must be > 0")
	}

	if err := validateHTTPURL(c.Hister.BaseURL); err != nil {
		validationErrs = append(validationErrs, fmt.Sprintf("hister.base_url: %v", err))
	}
	if err := validatePath(c.Hister.AddPath); err != nil {
		validationErrs = append(validationErrs, fmt.Sprintf("hister.add_path: %v", err))
	}
	if err := validatePath(c.Hister.SearchWSPath); err != nil {
		validationErrs = append(validationErrs, fmt.Sprintf("hister.search_ws_path: %v", err))
	}

	if c.HTTP.RequestTimeoutMS <= 0 {
		validationErrs = append(validationErrs, "http.request_timeout_ms must be > 0")
	}

	if strings.TrimSpace(c.Storage.StateDBPath) == "" {
		validationErrs = append(validationErrs, "storage.state_db_path is required")
	}
	if strings.TrimSpace(c.Storage.CryptoDBPath) == "" {
		validationErrs = append(validationErrs, "storage.crypto_db_path is required")
	}
	if c.Storage.StateDBPath != "" && c.Storage.StateDBPath == c.Storage.CryptoDBPath {
		validationErrs = append(validationErrs, "storage.state_db_path and storage.crypto_db_path must be different")
	}

	if len(validationErrs) > 0 {
		return fmt.Errorf("invalid config: %s", strings.Join(validationErrs, "; "))
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.Matrix.SyncTimeoutMS <= 0 {
		c.Matrix.SyncTimeoutMS = defaultSyncTimeoutMS
	}
	if strings.TrimSpace(c.Bot.SearchCommand) == "" {
		c.Bot.SearchCommand = defaultSearchCommand
	}
	if c.Bot.MaxResults <= 0 {
		c.Bot.MaxResults = defaultMaxResults
	}
	if strings.TrimSpace(c.Bot.ReplyMode) == "" {
		c.Bot.ReplyMode = defaultReplyMode
	}
	if c.Bot.MaxQueryLen <= 0 {
		c.Bot.MaxQueryLen = defaultMaxQueryLen
	}
	if strings.TrimSpace(c.Hister.AddPath) == "" {
		c.Hister.AddPath = defaultAddPath
	}
	if strings.TrimSpace(c.Hister.SearchWSPath) == "" {
		c.Hister.SearchWSPath = defaultSearchWSPath
	}
	if c.HTTP.RequestTimeoutMS <= 0 {
		c.HTTP.RequestTimeoutMS = defaultRequestTimeoutMS
	}
	if strings.TrimSpace(c.Storage.StateDBPath) == "" {
		c.Storage.StateDBPath = defaultStateDBPath
	}
	if strings.TrimSpace(c.Storage.CryptoDBPath) == "" {
		c.Storage.CryptoDBPath = defaultCryptoDBPath
	}
}

func (c Config) SyncTimeout() time.Duration {
	return time.Duration(c.Matrix.SyncTimeoutMS) * time.Millisecond
}

func (c Config) RequestTimeout() time.Duration {
	return time.Duration(c.HTTP.RequestTimeoutMS) * time.Millisecond
}

func resolvePath(base, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Clean(filepath.Join(base, path))
}

func validateHTTPURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("is required")
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return fmt.Errorf("must be a valid absolute URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("must use http or https")
	}
	if u.Host == "" {
		return errors.New("must include host")
	}
	return nil
}

func validatePath(p string) error {
	p = strings.TrimSpace(p)
	if p == "" {
		return errors.New("is required")
	}
	if !strings.HasPrefix(p, "/") {
		return errors.New("must start with '/'")
	}
	return nil
}
