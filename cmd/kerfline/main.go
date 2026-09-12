package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"github.com/timbze/kerfline/internal/bot"
	"github.com/timbze/kerfline/internal/config"
	"github.com/timbze/kerfline/internal/runner"
	"github.com/timbze/kerfline/internal/session"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	configDir := flag.String("config-dir", config.DefaultDir(), "config directory (config.toml + chats/)")
	flag.Parse()

	loadDotEnv(*configDir)

	cfg, err := config.Load(*configDir)
	if err != nil {
		return err
	}
	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		return fmt.Errorf("BOT_TOKEN is not set (put it in %s/env or the environment)", *configDir)
	}
	if secret := os.Getenv("TELEGRAM_WEBHOOK_SECRET"); secret != "" {
		cfg.Telegram.SecretToken = secret
	}

	tg, err := gotgbot.NewBot(token, &gotgbot.BotOpts{
		BotClient: bot.NewTelegramClient(),
		RequestOpts: &gotgbot.RequestOpts{
			Timeout: 15 * time.Second,
		},
	})
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	log.Info("authorized", "bot", tg.Username, "chats", len(cfg.Chats), "webhook", cfg.UseWebhook())

	store, err := session.Open(session.Path())
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return bot.New(cfg, log, runner.New(), store).Run(ctx, tg)
}

func loadDotEnv(dir string) {
	path := dir + "/env"
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range splitLines(string(raw)) {
		if line == "" || line[0] == '#' {
			continue
		}
		key, val, ok := splitKV(line)
		if !ok {
			continue
		}
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, trimCR(s[start:i]))
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, trimCR(s[start:]))
	}
	return out
}

func trimCR(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\r' {
		return s[:len(s)-1]
	}
	return s
}

func splitKV(line string) (string, string, bool) {
	for i := 0; i < len(line); i++ {
		if line[i] == '=' {
			key := line[:i]
			val := line[i+1:]
			if len(val) >= 2 {
				if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
					val = val[1 : len(val)-1]
				}
			}
			return key, val, key != ""
		}
	}
	return "", "", false
}
