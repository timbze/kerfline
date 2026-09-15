package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/timbze/kerfline/internal/config"
)

type Result struct {
	Stdout    string
	Stderr    string
	Duration  time.Duration
	Container string // Incus instance actually exec'd (repo-scoped full name)
}

type Request struct {
	Prompt     string
	PromptFile string
	Deny       []string
}

type Runner struct {
	LookPath func(string) (string, error)
	Command  func(ctx context.Context, name string, args ...string) *exec.Cmd
	// Resolve, if set, returns the Incus instance for a chat. Tests stub this
	// so they do not run `jailbee ls`. Production leaves it nil.
	Resolve func(ctx context.Context, cfg *config.Config, chat config.Chat) (string, error)

	mu       sync.Mutex
	resolved map[string]string
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

	grok := []string{"grok"}
	if req.PromptFile != "" {
		grok = append(grok, "--prompt-file", req.PromptFile)
	} else {
		grok = append(grok, "-p", req.Prompt)
	}
	if cfg.Grok.AlwaysApprove {
		grok = append(grok, "--always-approve")
	}
	if len(cfg.Grok.DisallowedTools) > 0 {
		grok = append(grok, "--disallowed-tools", strings.Join(cfg.Grok.DisallowedTools, ","))
	}
	if sessionID != "" {
		if resume {
			grok = append(grok, "-r", sessionID)
		} else {
			grok = append(grok, "-s", sessionID)
		}
	}
	for _, rule := range req.Deny {
		grok = append(grok, "--deny", rule)
	}
	grok = append(grok, cfg.Grok.ExtraArgs...)
	grok = append(grok, "--output-format", "streaming-json")
	if cfg.Rules != "" {
		grok = append(grok, "--rules", cfg.Rules)
	}

	cmd, incusName, err := r.jailbeeExec(ctx, cfg, chat, grok)
	if err != nil {
		return Result{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("stdout pipe: %w", err)
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{Stderr: strings.TrimSpace(stderr.String()), Duration: time.Since(start), Container: incusName}, fmt.Errorf("jailbee exec: %w", err)
	}
	out, _ := lastTurn(stdout)
	err = cmd.Wait()
	res := Result{Stdout: out, Stderr: strings.TrimSpace(stderr.String()), Duration: time.Since(start), Container: incusName}
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
	cmd, _, err := r.jailbeeExec(ctx, cfg, chat, []string{"cat", "--", containerPath})
	if err != nil {
		return nil, err
	}
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

func (r *Runner) RefreshGrokAuth(ctx context.Context, cfg *config.Config, chat config.Chat) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	cmd, _, err := r.jailbeeExec(ctx, cfg, chat, []string{"grok", "models"})
	if err != nil {
		return err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("jailbee exec: %w: %s", err, truncate(stderr.String(), 500))
		}
		return fmt.Errorf("jailbee exec: %w", err)
	}
	return nil
}

func (r *Runner) jailbeeExec(ctx context.Context, cfg *config.Config, chat config.Chat, inner []string) (*exec.Cmd, string, error) {
	bin := cfg.Jailbee.Binary
	path, err := r.LookPath(bin)
	if err != nil {
		return nil, "", fmt.Errorf("find %s: %w", bin, err)
	}
	incusName, err := r.containerName(ctx, cfg, chat)
	if err != nil {
		return nil, "", err
	}
	args := []string{"exec", "-c", chat.JailbeeConfig(), incusName, "--"}
	args = append(args, inner...)
	cmd := r.Command(ctx, path, args...)
	cmd.Dir = chat.Workspace
	cmd.Env = filteredEnv()
	return cmd, incusName, nil
}

func (r *Runner) ContainerName(ctx context.Context, cfg *config.Config, chat config.Chat) (string, error) {
	return r.containerName(ctx, cfg, chat)
}

func (r *Runner) containerName(ctx context.Context, cfg *config.Config, chat config.Chat) (string, error) {
	if r.Resolve != nil {
		return r.Resolve(ctx, cfg, chat)
	}
	key := chat.JailbeeConfig() + "\x00" + chat.JailbeeContainer
	r.mu.Lock()
	if name, ok := r.resolved[key]; ok {
		r.mu.Unlock()
		return name, nil
	}
	r.mu.Unlock()

	name, err := r.resolveFromList(ctx, cfg, chat)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	if r.resolved == nil {
		r.resolved = make(map[string]string)
	}
	r.resolved[key] = name
	r.mu.Unlock()
	return name, nil
}

type containerRow struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	State    string `json:"state"`
}

// resolveFromList maps jailbee_container to this workspace's Incus instance.
// JailBee's own exec resolver treats an exact Incus name as global, so a
// container named "main" in another repo would steal `jailbee exec main`.
func (r *Runner) resolveFromList(ctx context.Context, cfg *config.Config, chat config.Chat) (string, error) {
	bin := cfg.Jailbee.Binary
	path, err := r.LookPath(bin)
	if err != nil {
		return "", fmt.Errorf("find %s: %w", bin, err)
	}
	args := []string{
		"ls",
		"-c", chat.JailbeeConfig(),
		"--fields", "name,full_name,state",
		"-o", "json",
	}
	cmd := r.Command(ctx, path, args...)
	cmd.Dir = chat.Workspace
	cmd.Env = filteredEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("jailbee ls: %w: %s", err, truncate(stderr.String(), 500))
		}
		return "", fmt.Errorf("jailbee ls: %w", err)
	}
	var rows []containerRow
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		return "", fmt.Errorf("jailbee ls json: %w", err)
	}
	name, err := pickContainer(chat.JailbeeContainer, rows)
	if err != nil {
		return "", fmt.Errorf("%w (workspace %s)", err, chat.Workspace)
	}
	return name, nil
}

func pickContainer(want string, rows []containerRow) (string, error) {
	want = strings.TrimSpace(want)
	if want == "" {
		return "", fmt.Errorf("jailbee_container is empty")
	}
	var matches []string
	seen := make(map[string]struct{})
	for _, row := range rows {
		full := strings.TrimSpace(row.FullName)
		short := strings.TrimSpace(row.Name)
		if full == "" {
			full = short
		}
		if short != want && full != want {
			continue
		}
		if _, ok := seen[full]; ok {
			continue
		}
		seen[full] = struct{}{}
		matches = append(matches, full)
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("jailbee container %q is not in this workspace", want)
	case 1:
		if matches[0] == "" {
			return "", fmt.Errorf("jailbee container %q has an empty Incus name", want)
		}
		return matches[0], nil
	default:
		return "", fmt.Errorf("jailbee container %q is ambiguous: %s", want, strings.Join(matches, ", "))
	}
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
