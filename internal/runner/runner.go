package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"kerfline/internal/config"
)

type Result struct {
	Stdout   string
	Stderr   string
	Duration time.Duration
}

type Runner struct {
	LookPath func(string) (string, error)
	Command  func(ctx context.Context, name string, args ...string) *exec.Cmd
}

func New() *Runner {
	return &Runner{
		LookPath: exec.LookPath,
		Command:  exec.CommandContext,
	}
}

func (r *Runner) Run(ctx context.Context, cfg *config.Config, chat config.Chat, prompt string, sessionID string, resume bool) (Result, error) {
	bin := cfg.Jailbee.Binary
	path, err := r.LookPath(bin)
	if err != nil {
		return Result{}, fmt.Errorf("find %s: %w", bin, err)
	}

	timeout := cfg.Grok.Timeout
	if timeout != "" {
		d, err := time.ParseDuration(timeout)
		if err != nil {
			return Result{}, fmt.Errorf("grok.timeout: %w", err)
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}

	args := []string{
		"exec",
		"-c", chat.JailbeeConfig(),
		chat.JailbeeContainer,
		"--",
		"grok", "-p", prompt,
		"--output-format", "plain",
	}
	if cfg.Grok.AlwaysApprove {
		args = append(args, "--always-approve")
	}
	if len(cfg.Grok.DisallowedTools) > 0 {
		args = append(args, "--disallowed-tools", strings.Join(cfg.Grok.DisallowedTools, ","))
	}
	if sessionID != "" {
		if resume {
			args = append(args, "-r", sessionID)
		} else {
			args = append(args, "-s", sessionID)
		}
	}
	args = append(args, cfg.Grok.ExtraArgs...)
	if cfg.Rules != "" {
		args = append(args, "--rules", cfg.Rules)
	}

	cmd := r.Command(ctx, path, args...)
	cmd.Dir = chat.Workspace
	cmd.Env = filteredEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	start := time.Now()
	err = cmd.Run()
	res := Result{Stdout: strings.TrimSpace(stdout.String()), Stderr: strings.TrimSpace(stderr.String()), Duration: time.Since(start)}
	if err != nil {
		if res.Stderr != "" {
			return res, fmt.Errorf("jailbee exec: %w: %s", err, truncate(res.Stderr, 500))
		}
		return res, fmt.Errorf("jailbee exec: %w", err)
	}
	return res, nil
}

func filteredEnv() []string {
	allow := map[string]struct{}{
		"PATH": {}, "HOME": {}, "USER": {}, "LOGNAME": {},
		"LANG": {}, "LC_ALL": {}, "TERM": {}, "TMPDIR": {},
		"XDG_RUNTIME_DIR": {}, "XDG_CONFIG_HOME": {},
	}
	var out []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if _, ok := allow[key]; ok {
			out = append(out, kv)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
