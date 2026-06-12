package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type CookiePair struct {
	AuthToken string `yaml:"auth_token"`
	Ct0       string `yaml:"ct0"`
	Label     string `yaml:"label,omitempty"`
}

type TwitterConfig struct {
	Cookies              []CookiePair `yaml:"cookies"`
	HealthCheckInterval  string       `yaml:"health_check_interval,omitempty"`
	AlertWebhook         string       `yaml:"alert_webhook,omitempty"`
}

type TrackingConfig struct {
	PollInterval   string `yaml:"poll_interval"`
	TweetsPerCheck int    `yaml:"tweets_per_check"`
	// Adaptive polling: when idle, back off to IdlePollInterval
	IdlePollInterval string `yaml:"idle_poll_interval,omitempty"`
	// How long with no activity before switching to idle interval
	IdleThreshold string `yaml:"idle_threshold,omitempty"`
}

type DiscordConfig struct {
	BotToken       string `yaml:"bot_token"`
	GuildID        string `yaml:"guild_id,omitempty"`
	DefaultChannel string `yaml:"default_channel,omitempty"`
}

type LogConfig struct {
	Level    string `yaml:"level"`
	Timezone string `yaml:"timezone"`
}

type Config struct {
	Discord  DiscordConfig  `yaml:"discord"`
	Twitter  TwitterConfig  `yaml:"twitter"`
	Tracking TrackingConfig `yaml:"tracking"`
	Logging  LogConfig      `yaml:"logging"`
}

func (c *Config) PollIntervalDuration() time.Duration {
	d, err := time.ParseDuration(c.Tracking.PollInterval)
	if err != nil {
		return 60 * time.Second
	}
	return d
}

func (c *Config) Timezone() *time.Location {
	tz := c.Logging.Timezone
	if tz == "" {
		tz = "Asia/Jakarta"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.FixedZone("WIB", 7*3600)
	}
	return loc
}

func (c *Config) IdlePollIntervalDuration() time.Duration {
	d, err := time.ParseDuration(c.Tracking.IdlePollInterval)
	if err != nil {
		return 30 * time.Second
	}
	return d
}

func (c *Config) IdleThresholdDuration() time.Duration {
	d, err := time.ParseDuration(c.Tracking.IdleThreshold)
	if err != nil {
		return 5 * time.Minute
	}
	return d
}

func (c *Config) HealthCheckDuration() time.Duration {
	d, err := time.ParseDuration(c.Twitter.HealthCheckInterval)
	if err != nil {
		return 5 * time.Minute
	}
	return d
}

// CookieHash returns a sha256 hash of the first auth_token for change detection.
func (c *Config) CookieHash() string {
	if len(c.Twitter.Cookies) == 0 {
		return ""
	}
	h := sha256.Sum256([]byte(c.Twitter.Cookies[0].AuthToken))
	return fmt.Sprintf("%x", h[:8])
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Defaults
	if cfg.Tracking.PollInterval == "" {
		cfg.Tracking.PollInterval = "5s"
	}
	if cfg.Tracking.TweetsPerCheck == 0 {
		cfg.Tracking.TweetsPerCheck = 5
	}
	if cfg.Logging.Timezone == "" {
		cfg.Logging.Timezone = "Asia/Jakarta"
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Twitter.HealthCheckInterval == "" {
		cfg.Twitter.HealthCheckInterval = "5m"
	}

	return cfg, nil
}

func validateConfig(cfg *Config) error {
	if cfg.Discord.BotToken == "" {
		return fmt.Errorf("discord.bot_token required")
	}
	if cfg.Discord.DefaultChannel == "" {
		return fmt.Errorf("discord.default_channel required — set the channel ID where notifications will be sent")
	}
	if len(cfg.Twitter.Cookies) == 0 {
		return fmt.Errorf("twitter.cookies required (at least one)")
	}
	for i, c := range cfg.Twitter.Cookies {
		if c.AuthToken == "" || c.Ct0 == "" {
			return fmt.Errorf("cookie pair %d: auth_token and ct0 required", i+1)
		}
	}
	return nil
}

var handleRe = regexp.MustCompile(`(?:https?://)?(?:x\.com|twitter\.com)/@?([A-Za-z0-9_]+)`)

func normalizeHandle(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	m := handleRe.FindStringSubmatch(s)
	if m != nil && m[1] != "" {
		return m[1]
	}
	s = strings.TrimPrefix(s, "@")
	if regexp.MustCompile(`^[A-Za-z0-9_]+$`).MatchString(s) {
		return s
	}
	return ""
}

// DataDir returns the directory for storing state/watch files, next to the config.
func DataDir(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "data")
}
