package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadWebhook(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"config.toml": `
[telegram]
mode = "webhook"
listen = "127.0.0.1:8787"
public_url = "https://bot.example.com"
secret_token = "s3cret"
path = "/hook"

[grok]
always_approve = true
disallowed_tools = ["web_fetch"]
timeout = "5m"
`,
		"chats/notes.toml": `
name = "notes"
telegram_chat_id = -1001
allowed_user_ids = [42]
require_mention = true
workspace = "/tmp/notes"
jailbee_container = "main"
gitea_remote = "https://gitea.example.com/bot/notes"
tea_login = "notes-bot"
`,
	})
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UseWebhook() {
		t.Fatal("expected webhook")
	}
	if !cfg.Telegram.ShouldDrain() {
		t.Fatal("drain on start should default true")
	}
	ch, kind := cfg.LookupTelegram(-1001, 0)
	if kind != TelegramCatchAll {
		t.Fatalf("kind = %d, want catch-all", kind)
	}
	if !ch.AllowsUser(42) || ch.AllowsUser(7) {
		t.Fatal("allowlist")
	}
	if ch.JailbeeConfig() != "/tmp/notes/.jailbee/config.yaml" {
		t.Fatalf("config path: %s", ch.JailbeeConfig())
	}
	if ch.TeaLogin != "notes-bot" {
		t.Fatal("tea_login should round-trip for later wiring")
	}
}

func TestWebhookWithoutURLFallsBackToPoll(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"config.toml": `
[telegram]
mode = "webhook"
secret_token = "s3cret"
`,
		"chats/dm.toml": `
telegram_chat_id = 1
workspace = "/tmp/x"
`,
	})
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UseWebhook() {
		t.Fatal("empty public_url should not use webhook")
	}
}

func TestZeroChatIDSkipped(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"config.toml": `[telegram]
mode = "poll"`,
		"chats/pending.toml": "telegram_chat_id = 0\nworkspace = \"/tmp/x\"\n",
	})
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Chats) != 0 {
		t.Fatalf("pending chat should be skipped, got %d", len(cfg.Chats))
	}
}

func pollTree(t *testing.T, chats map[string]string) *Config {
	t.Helper()
	files := map[string]string{
		"config.toml": `[telegram]
mode = "poll"`,
	}
	for name, body := range chats {
		files["chats/"+name] = body
	}
	cfg, err := Load(writeTree(t, files))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestDuplicateChatID(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"config.toml": `[telegram]
mode = "poll"`,
		"chats/a.toml": "telegram_chat_id = 1\nworkspace = \"/a\"\n",
		"chats/b.toml": "telegram_chat_id = 1\nworkspace = \"/b\"\n",
	})
	if _, err := Load(dir); err == nil {
		t.Fatal("expected duplicate id error")
	}
}

func TestTwoTopicsSameGroup(t *testing.T) {
	cfg := pollTree(t, map[string]string{
		"ops.toml": "name = \"ops\"\ntelegram_chat_id = -1001\ntelegram_topic_id = 3\nworkspace = \"/ops\"\n",
		"dev.toml": "name = \"dev\"\ntelegram_chat_id = -1001\ntelegram_topic_id = 5\nworkspace = \"/dev\"\n",
	})
	if len(cfg.Chats) != 2 {
		t.Fatalf("got %d chats", len(cfg.Chats))
	}
	ch, kind := cfg.LookupTelegram(-1001, 3)
	if kind != TelegramExact || ch.Name != "ops" {
		t.Fatalf("topic 3: %+v kind %d", ch, kind)
	}
	ch, kind = cfg.LookupTelegram(-1001, 5)
	if kind != TelegramExact || ch.Name != "dev" {
		t.Fatalf("topic 5: %+v kind %d", ch, kind)
	}
}

func TestDuplicateTopicPairDifferentNames(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"config.toml": `[telegram]
mode = "poll"`,
		"chats/a.toml": "name = \"a\"\ntelegram_chat_id = -1001\ntelegram_topic_id = 3\nworkspace = \"/a\"\n",
		"chats/b.toml": "name = \"b\"\ntelegram_chat_id = -1001\ntelegram_topic_id = 3\nworkspace = \"/b\"\n",
	})
	if _, err := Load(dir); err == nil {
		t.Fatal("expected duplicate pair error")
	}
}

func TestCatchAllPlusTopic(t *testing.T) {
	cfg := pollTree(t, map[string]string{
		"notes.toml":     "name = \"notes\"\ntelegram_chat_id = -1001\nworkspace = \"/notes\"\n",
		"notes-ops.toml": "name = \"notes-ops\"\ntelegram_chat_id = -1001\ntelegram_topic_id = 3\nworkspace = \"/ops\"\n",
	})
	ch, kind := cfg.LookupTelegram(-1001, 3)
	if kind != TelegramExact || ch.Name != "notes-ops" {
		t.Fatalf("thread 3: %+v kind %d", ch, kind)
	}
	ch, kind = cfg.LookupTelegram(-1001, 9)
	if kind != TelegramCatchAll || ch.Name != "notes" {
		t.Fatalf("thread 9: %+v kind %d", ch, kind)
	}
	ch, kind = cfg.LookupTelegram(-1001, 0)
	if kind != TelegramCatchAll || ch.Name != "notes" {
		t.Fatalf("thread 0: %+v kind %d", ch, kind)
	}
}

func TestTopicOnlyIgnoredTopic(t *testing.T) {
	cfg := pollTree(t, map[string]string{
		"ops.toml": "name = \"ops\"\ntelegram_chat_id = -1001\ntelegram_topic_id = 5\nworkspace = \"/ops\"\n",
	})
	_, kind := cfg.LookupTelegram(-1001, 3)
	if kind != TelegramIgnoredTopic {
		t.Fatalf("thread 3 kind = %d, want ignored topic", kind)
	}
	ch, kind := cfg.LookupTelegram(-1001, 5)
	if kind != TelegramExact || ch.Name != "ops" {
		t.Fatalf("thread 5: %+v kind %d", ch, kind)
	}
}

func TestNegativeTopicID(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"config.toml": `[telegram]
mode = "poll"`,
		"chats/a.toml": "telegram_chat_id = -1001\ntelegram_topic_id = -1\nworkspace = \"/a\"\n",
	})
	if _, err := Load(dir); err == nil {
		t.Fatal("expected negative topic_id error")
	}
}

func TestLoadRulesMissingUsesDefault(t *testing.T) {
	cfg := pollTree(t, map[string]string{
		"a.toml": "telegram_chat_id = 1\nworkspace = \"/a\"\n",
	})
	want := strings.TrimSpace(DefaultRules)
	if want == "" {
		t.Fatal("DefaultRules must not be empty")
	}
	if cfg.Rules != want {
		t.Fatalf("Rules = %q, want default", cfg.Rules)
	}
	if !strings.Contains(want, "rich message") || !strings.Contains(want, "GitHub-flavored Markdown") {
		t.Fatalf("DefaultRules should tell Grok to write rich Markdown, got %q", want)
	}
	if strings.Contains(want, "does not render Markdown") {
		t.Fatal("DefaultRules must not claim Telegram is plain text")
	}
}

func TestLoadRulesFileReplacesDefault(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"config.toml": `[telegram]
mode = "poll"`,
		"chats/a.toml": "telegram_chat_id = 1\nworkspace = \"/a\"\n",
		"AGENTS.md":    "  Also be Finnish.  \n",
	})
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rules != "Also be Finnish." {
		t.Fatalf("Rules = %q", cfg.Rules)
	}
}

func TestLoadRulesEmptyFileUsesDefault(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"config.toml": `[telegram]
mode = "poll"`,
		"chats/a.toml": "telegram_chat_id = 1\nworkspace = \"/a\"\n",
		"AGENTS.md":    "  \n",
	})
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rules != strings.TrimSpace(DefaultRules) {
		t.Fatalf("empty AGENTS.md should keep default, got %q", cfg.Rules)
	}
}

func TestMediaDefaultsAndVision(t *testing.T) {
	cfg := pollTree(t, map[string]string{
		"a.toml": "telegram_chat_id = 1\nworkspace = \"/a\"\n",
	})
	if cfg.Media.MaxFileBytes != 20_000_000 {
		t.Fatalf("max_file_bytes %d", cfg.Media.MaxFileBytes)
	}
	if cfg.Media.Vision != VisionAuto {
		t.Fatalf("vision %q", cfg.Media.Vision)
	}
	if cfg.InboxTTL() != 168*time.Hour {
		t.Fatalf("ttl %s", cfg.InboxTTL())
	}
	if cfg.EffectiveVision(cfg.Chats[0]) != VisionAuto {
		t.Fatal("inherit")
	}
}

func TestChatVisionOverride(t *testing.T) {
	cfg := pollTree(t, map[string]string{
		"a.toml": "telegram_chat_id = 1\nworkspace = \"/a\"\nvision = \"never\"\n",
	})
	if cfg.EffectiveVision(cfg.Chats[0]) != VisionNever {
		t.Fatalf("got %q", cfg.EffectiveVision(cfg.Chats[0]))
	}
}

func TestUnknownVisionRejected(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"config.toml": `[telegram]
mode = "poll"
[media]
vision = "off"`,
		"chats/a.toml": "telegram_chat_id = 1\nworkspace = \"/a\"\n",
	})
	if _, err := Load(dir); err == nil {
		t.Fatal("expected unknown media.vision error")
	}
	dir = writeTree(t, map[string]string{
		"config.toml": `[telegram]
mode = "poll"`,
		"chats/a.toml": "telegram_chat_id = 1\nworkspace = \"/a\"\nvision = \"true\"\n",
	})
	if _, err := Load(dir); err == nil {
		t.Fatal("expected unknown chat vision error")
	}
}

func TestDefaultRulesIncludeAttachments(t *testing.T) {
	if !strings.Contains(DefaultRules, "Telegram attachments") {
		t.Fatal("DefaultRules must describe Telegram attachments")
	}
	if !strings.Contains(DefaultRules, "Never stage `.local/`") && !strings.Contains(DefaultRules, "Never\nstage `.local/`") {
		if !strings.Contains(DefaultRules, "telegram-inbox") {
			t.Fatal("DefaultRules must forbid staging telegram-inbox")
		}
	}
	if !strings.Contains(DefaultRules, "Sending files to Telegram") {
		t.Fatal("DefaultRules must describe sending workspace files back")
	}
	if !strings.Contains(DefaultRules, "![short caption](relative/path.jpg)") {
		t.Fatal("DefaultRules must show the markdown image send syntax")
	}
}

func TestUnknownChat(t *testing.T) {
	cfg := pollTree(t, map[string]string{
		"a.toml": "telegram_chat_id = 1\nworkspace = \"/a\"\n",
	})
	_, kind := cfg.LookupTelegram(99, 0)
	if kind != TelegramUnknownChat {
		t.Fatalf("kind = %d, want unknown chat", kind)
	}
}
