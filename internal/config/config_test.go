package config

import (
	"os"
	"path/filepath"
	"testing"
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
	ch, ok := cfg.ChatByID(-1001)
	if !ok {
		t.Fatal("missing chat")
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
