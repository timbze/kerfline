package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/timbze/kerfline/internal/config"
)

const defaultSandboxPATH = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

type Result struct {
	Stdout   string
	Stderr   string
	Duration time.Duration
	Sandbox  string // per-chat HOME inside the MXC sandbox
}

type Request struct {
	Prompt     string
	PromptFile string
	Deny       []string
	Effort     string // --reasoning-effort; empty = not passed
}

type Runner struct {
	LookPath func(string) (string, error)
	Command  func(ctx context.Context, name string, args ...string) *exec.Cmd
	// resolvFile overrides /etc/resolv.conf when tests need a fixture.
	resolvFile string
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
	if cfg == nil {
		return Result{}, fmt.Errorf("config is required")
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

	grokBin, err := r.grokBinary(cfg)
	if err != nil {
		return Result{}, err
	}
	grok := []string{grokBin}
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
	if req.Effort != "" {
		grok = append(grok, "--reasoning-effort", req.Effort)
	}
	grok = append(grok, cfg.Grok.ExtraArgs...)
	grok = append(grok, "--output-format", "streaming-json")
	if cfg.Rules != "" {
		grok = append(grok, "--rules", cfg.Rules)
	}

	cmd, home, cleanup, err := r.command(ctx, cfg, chat, grok)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("stdout pipe: %w", err)
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{Stderr: strings.TrimSpace(stderr.String()), Duration: time.Since(start), Sandbox: home}, fmt.Errorf("mxc: %w", err)
	}
	out, _ := lastTurn(stdout)
	err = cmd.Wait()
	res := Result{Stdout: out, Stderr: strings.TrimSpace(stderr.String()), Duration: time.Since(start), Sandbox: home}
	if err != nil {
		if res.Stderr != "" {
			return res, fmt.Errorf("mxc: %w: %s", err, truncate(res.Stderr, 500))
		}
		return res, fmt.Errorf("mxc: %w", err)
	}
	return res, nil
}

// ReadAuthFile reads this chat's Grok auth.json from the host sandbox home.
func (r *Runner) ReadAuthFile(_ context.Context, cfg *config.Config, chat config.Chat) ([]byte, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	path, err := cfg.AuthFile(chat)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
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
	grokBin, err := r.grokBinary(cfg)
	if err != nil {
		return err
	}
	cmd, _, cleanup, err := r.command(ctx, cfg, chat, []string{grokBin, "models"})
	if err != nil {
		return err
	}
	defer cleanup()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("mxc: %w: %s", err, truncate(stderr.String(), 500))
		}
		return fmt.Errorf("mxc: %w", err)
	}
	return nil
}

// Login runs `grok login --device-auth` inside the chat sandbox.
// Stdio is the caller's, so the device code is visible.
func (r *Runner) Login(ctx context.Context, cfg *config.Config, chat config.Chat) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
	}
	grokBin, err := r.grokBinary(cfg)
	if err != nil {
		return err
	}
	cmd, _, cleanup, err := r.command(ctx, cfg, chat, []string{grokBin, "login", "--device-auth"})
	if err != nil {
		return err
	}
	defer cleanup()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mxc: %w", err)
	}
	return nil
}

// Prepare checks the MXC executor and bubblewrap, then creates the chat's sandbox home.
func (r *Runner) Prepare(_ context.Context, cfg *config.Config, chat config.Chat) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("config is required")
	}
	if _, err := r.LookPath(cfg.MXC.Binary); err != nil {
		return "", fmt.Errorf("find %s: %w", cfg.MXC.Binary, err)
	}
	if _, err := r.LookPath("bwrap"); err != nil {
		return "", fmt.Errorf("find bwrap: %w", err)
	}
	if _, err := r.grokBinary(cfg); err != nil {
		return "", err
	}
	home, err := cfg.SandboxHome(chat)
	if err != nil {
		return "", err
	}
	grokDir := filepath.Join(home, ".grok")
	if err := os.MkdirAll(grokDir, 0o700); err != nil {
		return "", err
	}
	for _, dir := range []string{filepath.Dir(home), home, grokDir} {
		if err := os.Chmod(dir, 0o700); err != nil {
			return "", err
		}
	}
	return home, nil
}

func (r *Runner) command(ctx context.Context, cfg *config.Config, chat config.Chat, argv []string) (*exec.Cmd, string, func(), error) {
	noop := func() {}
	home, err := r.Prepare(ctx, cfg, chat)
	if err != nil {
		return nil, "", noop, err
	}
	body, err := r.requestJSON(cfg, chat, home, argv, timeoutMs(ctx))
	if err != nil {
		return nil, home, noop, err
	}
	f, err := os.CreateTemp("", "kerfline-mxc-*.json")
	if err != nil {
		return nil, home, noop, err
	}
	cleanup := func() { _ = os.Remove(f.Name()) }
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		cleanup()
		return nil, home, noop, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return nil, home, noop, err
	}
	bin, err := r.LookPath(cfg.MXC.Binary)
	if err != nil {
		cleanup()
		return nil, home, noop, fmt.Errorf("find %s: %w", cfg.MXC.Binary, err)
	}
	src, dst, ok, err := r.sandboxResolv()
	if err != nil {
		cleanup()
		return nil, home, noop, err
	}
	if !ok {
		cmd := r.Command(ctx, bin, "--config", f.Name())
		return cmd, home, cleanup, nil
	}
	unshare, err := r.LookPath("unshare")
	if err != nil {
		cleanup()
		return nil, home, noop, fmt.Errorf("find unshare: %w", err)
	}
	// unshare's mount namespace is private to this process, so the bind
	// does not change the host's resolver. lxc-exec then bind-mounts the
	// stub's directory into the sandbox and the workload sees src.
	cmd := r.Command(ctx, unshare,
		"--user", "--map-root-user", "--mount", "--propagation", "private",
		"--", "sh", "-c", sandboxResolvScript,
		"sh", src, dst, bin, f.Name(),
	)
	return cmd, home, cleanup, nil
}

func timeoutMs(ctx context.Context) int64 {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	ms := time.Until(deadline).Milliseconds()
	if ms < 1 {
		return 1
	}
	return ms
}

type mxcRequest struct {
	Version     string `json:"version"`
	Containment string `json:"containment"`
	Process     struct {
		CommandLine       string   `json:"commandLine"`
		Cwd               string   `json:"cwd"`
		Env               []string `json:"env"`
		InheritDefaultEnv bool     `json:"inheritDefaultEnv"`
		Timeout           int64    `json:"timeout,omitempty"`
	} `json:"process"`
	Filesystem struct {
		ReadwritePaths []string `json:"readwritePaths"`
		ReadonlyPaths  []string `json:"readonlyPaths"`
	} `json:"filesystem"`
	Network struct {
		Egress struct {
			Default string `json:"default"`
		} `json:"egress"`
		Ingress struct {
			Default      string `json:"default"`
			HostLoopback string `json:"hostLoopback"`
		} `json:"ingress"`
	} `json:"network"`
}

func (r *Runner) requestJSON(cfg *config.Config, chat config.Chat, home string, argv []string, timeout int64) ([]byte, error) {
	ws, err := filepath.Abs(chat.Workspace)
	if err != nil {
		return nil, err
	}
	ro, err := r.readonlyPaths(cfg, home)
	if err != nil {
		return nil, err
	}
	grokBin, err := r.grokBinary(cfg)
	if err != nil {
		return nil, err
	}
	var req mxcRequest
	req.Version = "1.0.0"
	req.Containment = "bubblewrap"
	req.Process.CommandLine = shellQuote(argv)
	req.Process.Cwd = ws
	req.Process.Env = []string{
		"HOME=" + home,
		"PATH=" + filepath.Dir(grokBin) + ":" + defaultSandboxPATH,
	}
	req.Process.InheritDefaultEnv = true
	req.Process.Timeout = timeout
	req.Filesystem.ReadwritePaths = []string{ws, home}
	req.Filesystem.ReadonlyPaths = ro
	req.Network.Egress.Default = "allow"
	req.Network.Ingress.Default = "deny"
	req.Network.Ingress.HostLoopback = "deny"
	return json.Marshal(req)
}

func (r *Runner) readonlyPaths(cfg *config.Config, home string) ([]string, error) {
	grokBin, err := r.grokBinary(cfg)
	if err != nil {
		return nil, err
	}
	binDir := filepath.Dir(grokBin)
	if err := refuseAuthDir(binDir); err != nil {
		return nil, fmt.Errorf("mxc.grok_bin: %w", err)
	}
	ro := []string{binDir}
	parent := filepath.Dir(binDir)
	for _, name := range []string{"bundled", "vendor", "docs"} {
		p := filepath.Join(parent, name)
		st, err := os.Stat(p)
		if err != nil || !st.IsDir() {
			continue
		}
		if err := refuseAuthDir(p); err != nil {
			return nil, err
		}
		ro = append(ro, p)
	}
	for _, p := range cfg.MXC.ReadonlyPaths {
		p = filepath.Clean(p)
		if p == home || strings.HasPrefix(p, home+string(filepath.Separator)) {
			continue
		}
		if err := refuseAuthDir(p); err != nil {
			return nil, fmt.Errorf("mxc.readonly_paths: %w", err)
		}
		ro = append(ro, p)
	}
	return dedupe(ro), nil
}

func refuseAuthDir(dir string) error {
	st, err := os.Stat(filepath.Join(dir, "auth.json"))
	if err != nil {
		return nil
	}
	if !st.IsDir() {
		return fmt.Errorf("refusing to mount %s because it contains auth.json", dir)
	}
	return nil
}

func dedupe(in []string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, p := range in {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func (r *Runner) grokBinary(cfg *config.Config) (string, error) {
	var p string
	if cfg.MXC.GrokBin != "" {
		if !filepath.IsAbs(cfg.MXC.GrokBin) {
			return "", fmt.Errorf("mxc.grok_bin must be absolute")
		}
		p = cfg.MXC.GrokBin
	} else {
		found, err := r.LookPath("grok")
		if err != nil {
			return "", fmt.Errorf("find grok: %w", err)
		}
		p = found
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("grok path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve grok: %w", err)
	}
	// A version-manager shim is often named grok but is a symlink to another
	// program. That program cannot see the host toolchain inside the sandbox.
	if !strings.Contains(strings.ToLower(filepath.Base(resolved)), "grok") {
		return "", fmt.Errorf("grok resolved to %s, which is not a grok binary; set mxc.grok_bin to the grok executable", resolved)
	}
	return resolved, nil
}

func shellQuote(args []string) string {
	var b strings.Builder
	for i, a := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte('\'')
		b.WriteString(strings.ReplaceAll(a, "'", `'\''`))
		b.WriteByte('\'')
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
