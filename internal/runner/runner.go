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

type Request struct {
	Prompt     string
	PromptFile string
	Deny       []string
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

func (r *Runner) Run(ctx context.Context, cfg *config.Config, chat config.Chat, req Request, sessionID string, resume bool) (Result, error) {
	if req.Prompt != "" && req.PromptFile != "" {
		return Result{}, fmt.Errorf("prompt and prompt-file are mutually exclusive")
	}
	if req.Prompt == "" && req.PromptFile == "" {
		return Result{}, fmt.Errorf("prompt is required")
	}

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
		"grok",
	}
	if req.PromptFile != "" {
		args = append(args, "--prompt-file", req.PromptFile)
	} else {
		args = append(args, "-p", req.Prompt)
	}
	args = append(args, "--output-format", "plain")
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
	for _, rule := range req.Deny {
		args = append(args, "--deny", rule)
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

func (r *Runner) ReadContainerFile(ctx context.Context, cfg *config.Config, chat config.Chat, containerPath string) ([]byte, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if !strings.HasPrefix(containerPath, "/") || strings.Contains(containerPath, "..") {
		return nil, fmt.Errorf("container path must be absolute")
	}
	bin := cfg.Jailbee.Binary
	path, err := r.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("find %s: %w", bin, err)
	}
	args := []string{
		"exec",
		"-c", chat.JailbeeConfig(),
		chat.JailbeeContainer,
		"--",
		"cat", "--", containerPath,
	}
	cmd := r.Command(ctx, path, args...)
	cmd.Dir = chat.Workspace
	cmd.Env = filteredEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("jailbee exec: %w: %s", err, truncate(stderr.String(), 500))
		}
		return nil, fmt.Errorf("jailbee exec: %w", err)
	}
	return stdout.Bytes(), nil
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
