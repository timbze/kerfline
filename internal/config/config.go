package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// DefaultRules is compiled into the binary and used when AGENTS.md is absent.
//
//go:embed agents.md
var DefaultRules string

const (
	ModeWebhook = "webhook"
	ModePoll    = "poll"

	VisionAuto   = "auto"
	VisionAlways = "always"
	VisionNever  = "never"

	defaultMaxFileBytes int64 = 20_000_000
	defaultInboxTTL           = "168h"
)

type Config struct {
	Telegram Telegram `toml:"telegram"`
	Grok     Grok     `toml:"grok"`
	Jailbee  Jailbee  `toml:"jailbee"`
	Media    Media    `toml:"media"`
	Chats    []Chat   `toml:"-"`
	Dir      string   `toml:"-"`
	// Rules is passed to grok --rules: AGENTS.md if present, otherwise DefaultRules.
	Rules string `toml:"-"`
}

type Telegram struct {
	// Mode is "webhook" (default) or "poll".
	Mode string `toml:"mode"`
	// Listen is the local bind address for the webhook server.
	Listen string `toml:"listen"`
	// PublicURL is the HTTPS origin Telegram should POST to (no trailing path).
	// Empty with mode=webhook falls back to long poll until a URL exists.
	PublicURL string `toml:"public_url"`
	// SecretToken is sent as X-Telegram-Bot-Api-Secret-Token. Required for webhook.
	SecretToken string `toml:"secret_token"`
	// Path is the URL path Telegram posts to (no leading slash required).
	Path string `toml:"path"`
	// DrainOnStart polls getUpdates after deleting the webhook so messages
	// queued while the bot was down are processed before setWebhook.
	DrainOnStart *bool `toml:"drain_on_start"`
}

func (t Telegram) ShouldDrain() bool {
	if t.DrainOnStart == nil {
		return true
	}
	return *t.DrainOnStart
}

type Grok struct {
	AlwaysApprove   bool     `toml:"always_approve"`
	DisallowedTools []string `toml:"disallowed_tools"`
	ExtraArgs       []string `toml:"extra_args"`
	Timeout         string   `toml:"timeout"`
}

type Jailbee struct {
	Binary string `toml:"binary"`
}

type Media struct {
	MaxFileBytes int64  `toml:"max_file_bytes"`
	Vision       string `toml:"vision"`
	InboxTTL     string `toml:"inbox_ttl"`
}

type Chat struct {
	Name             string  `toml:"name"`
	TelegramChatID   int64   `toml:"telegram_chat_id"`
	TelegramTopicID  int64   `toml:"telegram_topic_id"` // 0 = whole chat
	AllowedUserIDs   []int64 `toml:"allowed_user_ids"`
	RequireMention   bool    `toml:"require_mention"`
	Workspace        string  `toml:"workspace"`
	JailbeeContainer string  `toml:"jailbee_container"`
	GiteaRemote      string  `toml:"gitea_remote"`
	TeaLogin         string  `toml:"tea_login"`
	// Vision overrides [media].vision for this chat. Empty inherits.
	Vision string `toml:"vision"`
}

type TelegramKind int

const (
	TelegramUnknownChat  TelegramKind = iota // no Chat with this telegram_chat_id
	TelegramIgnoredTopic                     // some Chat has this chat_id; none matched thread
	TelegramExact                            // TelegramTopicID == threadID && threadID != 0
	TelegramCatchAll                         // TelegramTopicID == 0
)

type tgKey struct {
	chatID  int64
	topicID int64
}

func DefaultDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "kerfline")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "kerfline")
}

func Load(dir string) (*Config, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	cfgPath := filepath.Join(dir, "config.toml")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", cfgPath, err)
	}
	cfg := &Config{Dir: dir}
	if err := toml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", cfgPath, err)
	}
	applyDefaults(cfg)
	if err := loadChats(cfg, filepath.Join(dir, "chats")); err != nil {
		return nil, err
	}
	if err := loadRules(cfg); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func loadRules(cfg *Config) error {
	cfg.Rules = strings.TrimSpace(DefaultRules)
	path := filepath.Join(cfg.Dir, "AGENTS.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	if text := strings.TrimSpace(string(raw)); text != "" {
		cfg.Rules = text
	}
	return nil
}

func applyDefaults(cfg *Config) {
	if cfg.Telegram.Mode == "" {
		cfg.Telegram.Mode = ModeWebhook
	}
	cfg.Telegram.Mode = strings.ToLower(cfg.Telegram.Mode)
	if cfg.Telegram.Listen == "" {
		cfg.Telegram.Listen = "127.0.0.1:8787"
	}
	if cfg.Telegram.Path == "" {
		cfg.Telegram.Path = "hook"
	}
	cfg.Telegram.Path = strings.Trim(cfg.Telegram.Path, "/")
	cfg.Telegram.PublicURL = strings.TrimRight(cfg.Telegram.PublicURL, "/")
	if cfg.Grok.Timeout == "" {
		cfg.Grok.Timeout = "10m"
	}
	if cfg.Jailbee.Binary == "" {
		cfg.Jailbee.Binary = "jailbee"
	}
	if cfg.Media.MaxFileBytes == 0 {
		cfg.Media.MaxFileBytes = defaultMaxFileBytes
	}
	if cfg.Media.Vision == "" {
		cfg.Media.Vision = VisionAuto
	}
	cfg.Media.Vision = strings.ToLower(cfg.Media.Vision)
	if cfg.Media.InboxTTL == "" {
		cfg.Media.InboxTTL = defaultInboxTTL
	}
}

func loadChats(cfg *Config, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no chats directory at %s", dir)
		}
		return err
	}
	seenIDs := map[tgKey]string{}
	seenNames := map[string]struct{}{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var chat Chat
		if err := toml.Unmarshal(raw, &chat); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if chat.Name == "" {
			chat.Name = strings.TrimSuffix(e.Name(), ".toml")
		}
		if chat.JailbeeContainer == "" {
			chat.JailbeeContainer = "main"
		}
		if chat.Workspace == "" {
			return fmt.Errorf("%s: workspace is required", path)
		}
		if chat.TelegramTopicID < 0 {
			return fmt.Errorf("%s: telegram_topic_id must be >= 0", path)
		}
		chat.Vision = strings.ToLower(strings.TrimSpace(chat.Vision))
		if chat.TelegramChatID == 0 {
			// Placeholder until /chatid is pasted in. Skip, don't fail startup.
			continue
		}
		key := tgKey{chat.TelegramChatID, chat.TelegramTopicID}
		if prev, ok := seenIDs[key]; ok {
			return fmt.Errorf("duplicate telegram_chat_id %d topic_id %d (%s and %s)", chat.TelegramChatID, chat.TelegramTopicID, prev, chat.Name)
		}
		if _, ok := seenNames[chat.Name]; ok {
			return fmt.Errorf("duplicate chat name %q", chat.Name)
		}
		seenIDs[key] = chat.Name
		seenNames[chat.Name] = struct{}{}
		cfg.Chats = append(cfg.Chats, chat)
	}
	if len(entries) == 0 {
		return fmt.Errorf("no chats/*.toml found in %s", dir)
	}
	return nil
}

func (c *Config) validate() error {
	switch c.Telegram.Mode {
	case ModeWebhook, ModePoll:
	default:
		return fmt.Errorf("telegram.mode must be %q or %q", ModeWebhook, ModePoll)
	}
	if c.UseWebhook() && c.Telegram.SecretToken == "" {
		return fmt.Errorf("telegram.secret_token is required for webhook mode")
	}
	if err := validVision(c.Media.Vision, "media.vision"); err != nil {
		return err
	}
	if _, err := time.ParseDuration(c.Media.InboxTTL); err != nil {
		return fmt.Errorf("media.inbox_ttl: %w", err)
	}
	for _, ch := range c.Chats {
		if ch.Vision == "" {
			continue
		}
		if err := validVision(ch.Vision, fmt.Sprintf("chat %q vision", ch.Name)); err != nil {
			return err
		}
	}
	return nil
}

func validVision(v, field string) error {
	switch v {
	case VisionAuto, VisionAlways, VisionNever:
		return nil
	default:
		return fmt.Errorf("%s must be %q, %q, or %q", field, VisionAuto, VisionAlways, VisionNever)
	}
}

func (c *Config) EffectiveVision(ch Chat) string {
	if ch.Vision != "" {
		return ch.Vision
	}
	if c.Media.Vision != "" {
		return c.Media.Vision
	}
	return VisionAuto
}

func (c *Config) InboxTTL() time.Duration {
	d, err := time.ParseDuration(c.Media.InboxTTL)
	if err != nil || d <= 0 {
		return 168 * time.Hour
	}
	return d
}

// UseWebhook is true when webhook is requested and a public HTTPS URL is set.
func (c *Config) UseWebhook() bool {
	return c.Telegram.Mode == ModeWebhook && c.Telegram.PublicURL != ""
}

func (c *Config) LookupTelegram(chatID, threadID int64) (Chat, TelegramKind) {
	var catchAll Chat
	haveCatchAll := false
	sawChatID := false
	for _, ch := range c.Chats {
		if ch.TelegramChatID != chatID {
			continue
		}
		sawChatID = true
		if threadID != 0 && ch.TelegramTopicID == threadID {
			return ch, TelegramExact
		}
		if ch.TelegramTopicID == 0 {
			catchAll = ch
			haveCatchAll = true
		}
	}
	if haveCatchAll {
		return catchAll, TelegramCatchAll
	}
	if sawChatID {
		return Chat{}, TelegramIgnoredTopic
	}
	return Chat{}, TelegramUnknownChat
}

func (ch Chat) AllowsUser(userID int64) bool {
	if len(ch.AllowedUserIDs) == 0 {
		return true
	}
	for _, id := range ch.AllowedUserIDs {
		if id == userID {
			return true
		}
	}
	return false
}

func (ch Chat) JailbeeConfig() string {
	return filepath.Join(ch.Workspace, ".jailbee", "config.yaml")
}
