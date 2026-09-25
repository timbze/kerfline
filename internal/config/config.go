package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	STT      STT      `toml:"stt"`
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
	Gate            *bool    `toml:"gate"`
	GateModel       string   `toml:"gate_model"`
	GateTimeout     string   `toml:"gate_timeout"`
	GateReasoning   string   `toml:"gate_reasoning"`
	GateSpeechMax   string   `toml:"gate_speech_max"`
	// ReasoningLevels are the --reasoning-effort values the reply model may
	// run at. Two or more: the gate picks one per turn. One: always that.
	// Empty: the flag is not passed.
	ReasoningLevels []string `toml:"reasoning_levels"`
	// ReasoningDefault is used when the gate can't pick. Empty = lowest level.
	ReasoningDefault string `toml:"reasoning_default"`
}

// ReasoningEfforts lists the levels grok --reasoning-effort accepts, lowest first.
var ReasoningEfforts = []string{"low", "medium", "high", "xhigh"}

// normalizeLevels lowercases, dedupes, and orders levels lowest first.
// Unknown values sort last and are left for validate to reject.
func normalizeLevels(in []string) []string {
	var out []string
	for _, l := range in {
		l = strings.ToLower(strings.TrimSpace(l))
		if l != "" && !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	rank := func(l string) int {
		if i := slices.Index(ReasoningEfforts, l); i >= 0 {
			return i
		}
		return len(ReasoningEfforts)
	}
	slices.SortStableFunc(out, func(a, b string) int { return rank(a) - rank(b) })
	return out
}

// GateEnabled is true unless gate is explicitly false.
func (g Grok) GateEnabled() bool {
	if g.Gate == nil {
		return true
	}
	return *g.Gate
}

// SpeechMax is the longest voice/audio clip transcribed before the reply gate.
func (g Grok) SpeechMax() time.Duration {
	if g.GateSpeechMax == "" {
		return 2 * time.Minute
	}
	d, err := time.ParseDuration(g.GateSpeechMax)
	if err != nil || d <= 0 {
		return 2 * time.Minute
	}
	return d
}

type Jailbee struct {
	Binary string `toml:"binary"`
}

type Media struct {
	MaxFileBytes int64  `toml:"max_file_bytes"`
	Vision       string `toml:"vision"`
	InboxTTL     string `toml:"inbox_ttl"`
}

type STT struct {
	BaseURL  string `toml:"base_url"`
	Timeout  string `toml:"timeout"`
	AuthPath string `toml:"auth_path"`
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
	// ReasoningLevels overrides [grok].reasoning_levels for this chat. Empty inherits.
	ReasoningLevels []string `toml:"reasoning_levels"`
	// ReasoningDefault overrides [grok].reasoning_default for this chat.
	ReasoningDefault string `toml:"reasoning_default"`
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
	if cfg.Grok.GateModel == "" {
		cfg.Grok.GateModel = "grok-4.3"
	}
	if cfg.Grok.GateTimeout == "" {
		cfg.Grok.GateTimeout = "15s"
	}
	if cfg.Grok.GateReasoning == "" {
		cfg.Grok.GateReasoning = "none"
	}
	cfg.Grok.GateReasoning = strings.ToLower(cfg.Grok.GateReasoning)
	if cfg.Grok.GateSpeechMax == "" {
		cfg.Grok.GateSpeechMax = "2m"
	}
	cfg.Grok.ReasoningLevels = normalizeLevels(cfg.Grok.ReasoningLevels)
	cfg.Grok.ReasoningDefault = strings.ToLower(strings.TrimSpace(cfg.Grok.ReasoningDefault))
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
	if cfg.STT.BaseURL == "" {
		cfg.STT.BaseURL = "https://api.x.ai"
	}
	cfg.STT.BaseURL = strings.TrimRight(cfg.STT.BaseURL, "/")
	if cfg.STT.Timeout == "" {
		cfg.STT.Timeout = "2m"
	}
	if cfg.STT.AuthPath == "" {
		cfg.STT.AuthPath = "/home/dev/.grok/auth.json"
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
		chat.ReasoningLevels = normalizeLevels(chat.ReasoningLevels)
		chat.ReasoningDefault = strings.ToLower(strings.TrimSpace(chat.ReasoningDefault))
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
	if _, err := time.ParseDuration(c.STT.Timeout); err != nil {
		return fmt.Errorf("stt.timeout: %w", err)
	}
	if _, err := time.ParseDuration(c.Grok.GateTimeout); err != nil {
		return fmt.Errorf("grok.gate_timeout: %w", err)
	}
	if d, err := time.ParseDuration(c.Grok.GateSpeechMax); err != nil {
		return fmt.Errorf("grok.gate_speech_max: %w", err)
	} else if d <= 0 {
		return fmt.Errorf("grok.gate_speech_max must be > 0")
	}
	switch c.Grok.GateReasoning {
	case "none", "low", "medium", "high", "xhigh", "omit":
	default:
		return fmt.Errorf("grok.gate_reasoning must be none, low, medium, high, xhigh, or omit")
	}
	if !strings.HasPrefix(c.STT.AuthPath, "/") || strings.Contains(c.STT.AuthPath, "..") {
		return fmt.Errorf("stt.auth_path must be an absolute container path")
	}
	if err := validReasoning(c.Grok.ReasoningLevels, c.Grok.ReasoningDefault, "grok"); err != nil {
		return err
	}
	levelsUsed := len(c.Grok.ReasoningLevels) > 0
	for _, ch := range c.Chats {
		if ch.Vision != "" {
			if err := validVision(ch.Vision, fmt.Sprintf("chat %q vision", ch.Name)); err != nil {
				return err
			}
		}
		levels, def := c.Reasoning(ch)
		if ch.ReasoningDefault != "" && !slices.Contains(levels, def) {
			return fmt.Errorf("chat %q reasoning_default %q is not in its reasoning_levels", ch.Name, def)
		}
		if err := validReasoning(ch.ReasoningLevels, "", fmt.Sprintf("chat %q", ch.Name)); err != nil {
			return err
		}
		levelsUsed = levelsUsed || len(levels) > 0
	}
	if levelsUsed {
		for _, a := range c.Grok.ExtraArgs {
			if a == "--reasoning-effort" || a == "--effort" ||
				strings.HasPrefix(a, "--reasoning-effort=") || strings.HasPrefix(a, "--effort=") {
				return fmt.Errorf("grok.extra_args sets %s; remove it when reasoning_levels is set", a)
			}
		}
	}
	return nil
}

func validReasoning(levels []string, def, field string) error {
	for _, l := range levels {
		if !slices.Contains(ReasoningEfforts, l) {
			return fmt.Errorf("%s.reasoning_levels: %q must be one of %s", field, l, strings.Join(ReasoningEfforts, ", "))
		}
	}
	if def != "" && !slices.Contains(levels, def) {
		return fmt.Errorf("%s.reasoning_default %q is not in reasoning_levels", field, def)
	}
	return nil
}

// Reasoning returns the chat's allowed reasoning levels (lowest first) and
// the level to use when the gate can't pick. Both are empty when unset.
func (c *Config) Reasoning(ch Chat) ([]string, string) {
	levels, def := c.Grok.ReasoningLevels, c.Grok.ReasoningDefault
	if len(ch.ReasoningLevels) > 0 {
		levels = ch.ReasoningLevels
		if !slices.Contains(levels, def) {
			def = ""
		}
	}
	if ch.ReasoningDefault != "" {
		def = ch.ReasoningDefault
	}
	if def == "" && len(levels) > 0 {
		def = levels[0]
	}
	return levels, def
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

func (c *Config) STTTimeout() time.Duration {
	d, err := time.ParseDuration(c.STT.Timeout)
	if err != nil || d <= 0 {
		return 2 * time.Minute
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
