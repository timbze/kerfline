package runner

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timbze/kerfline/internal/config"
)

func testRig(t *testing.T) (*Runner, *config.Config, config.Chat, string) {
	t.Helper()
	root := t.TempDir()
	install := t.TempDir()
	binDir := filepath.Join(install, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	grok := filepath.Join(binDir, "grok")
	if err := os.WriteFile(grok, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(install, "bundled"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		MXC: config.MXC{
			Binary:   "lxc-exec",
			GrokBin:  grok,
			StateDir: root,
		},
	}
	chat := config.Chat{Name: "notes", Workspace: t.TempDir()}
	r := New()
	r.LookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	resolv := filepath.Join(root, "resolv.conf")
	if err := os.WriteFile(resolv, []byte("nameserver 1.1.1.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.resolvFile = resolv
	return r, cfg, chat, install
}

func captureReq(t *testing.T, r *Runner) *mxcRequest {
	t.Helper()
	got := &mxcRequest{}
	r.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name != "/usr/bin/lxc-exec" {
			t.Fatalf("bin %s", name)
		}
		raw, err := os.ReadFile(configPath(t, args))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, got); err != nil {
			t.Fatal(err)
		}
		return exec.CommandContext(ctx, "true")
	}
	return got
}

func configPath(t *testing.T, args []string) string {
	t.Helper()
	for i, a := range args {
		if a == "--config" && i+1 < len(args) {
			return args[i+1]
		}
	}
	t.Fatalf("no --config in %q", strings.Join(args, " "))
	return ""
}

func TestRunBuildsMXCRequest(t *testing.T) {
	r, cfg, chat, install := testRig(t)
	cfg.Grok = config.Grok{
		AlwaysApprove:   true,
		DisallowedTools: []string{"web_fetch"},
		Timeout:         "1m",
	}
	cfg.Rules = "Keep it short."
	got := captureReq(t, r)
	if _, err := r.Run(context.Background(), cfg, chat, Request{Prompt: "ping"}, "11111111-1111-1111-1111-111111111111", true); err != nil {
		t.Fatal(err)
	}
	line := got.Process.CommandLine
	for _, want := range []string{
		filepath.Join(install, "bin", "grok"),
		"-p",
		"ping",
		"--always-approve",
		"--disallowed-tools",
		"web_fetch",
		"-r",
		"11111111-1111-1111-1111-111111111111",
		"--output-format",
		"streaming-json",
		"--rules",
		"Keep it short.",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("command %q missing %q", line, want)
		}
	}
	if got.Version != "1.0.0" || got.Containment != "bubblewrap" {
		t.Fatalf("request %+v", got)
	}
	if got.Process.Cwd != chat.Workspace {
		t.Fatalf("cwd %s", got.Process.Cwd)
	}
	home, err := cfg.SandboxHome(chat)
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(got.Filesystem.ReadwritePaths, chat.Workspace, home) {
		t.Fatalf("readwrite %v", got.Filesystem.ReadwritePaths)
	}
	if !containsAll(got.Filesystem.ReadonlyPaths, filepath.Join(install, "bin"), filepath.Join(install, "bundled")) {
		t.Fatalf("readonly %v", got.Filesystem.ReadonlyPaths)
	}
	if got.Network.Egress.Default != "allow" || got.Network.Ingress.Default != "deny" || got.Network.Ingress.HostLoopback != "deny" {
		t.Fatalf("network %+v", got.Network)
	}
	joinedEnv := strings.Join(got.Process.Env, "\n")
	if !strings.Contains(joinedEnv, "HOME="+home) {
		t.Fatalf("env %s", joinedEnv)
	}
	if strings.Contains(joinedEnv, "XDG_CONFIG_HOME") || strings.Contains(joinedEnv, "XDG_RUNTIME_DIR") {
		t.Fatalf("env leaks host dirs: %s", joinedEnv)
	}
}

func TestPolicyHidesHostGrokAndOtherChats(t *testing.T) {
	r, cfg, chat, _ := testRig(t)
	other := filepath.Join(cfg.MXC.StateDir, "hd", "home")
	hostGrok := filepath.Join(t.TempDir(), ".grok")
	cfg.MXC.ReadonlyPaths = []string{hostGrok}
	if err := os.MkdirAll(hostGrok, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostGrok, "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := captureReq(t, r)
	_, err := r.Run(context.Background(), cfg, chat, Request{Prompt: "ping"}, "", false)
	if err == nil || !strings.Contains(err.Error(), "auth.json") {
		t.Fatalf("err %v", err)
	}
	cfg.MXC.ReadonlyPaths = nil
	if _, err := r.Run(context.Background(), cfg, chat, Request{Prompt: "ping"}, "", false); err != nil {
		t.Fatal(err)
	}
	blob := strings.Join(append(got.Filesystem.ReadwritePaths, got.Filesystem.ReadonlyPaths...), "\n")
	for _, hidden := range []string{hostGrok, other, filepath.Join(t.TempDir(), ".config", "kerfline")} {
		if strings.Contains(blob, hidden) {
			t.Fatalf("policy exposes %s: %s", hidden, blob)
		}
	}
}

func TestGrokBinaryResolvesSymlinkAndRejectsShim(t *testing.T) {
	r, cfg, chat, install := testRig(t)
	binDir := filepath.Join(install, "bin")
	real := filepath.Join(binDir, "grok-1.0.46")
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(binDir, "grok")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("grok-1.0.46", link); err != nil {
		t.Fatal(err)
	}
	cfg.MXC.GrokBin = link
	got := captureReq(t, r)
	if _, err := r.Run(context.Background(), cfg, chat, Request{Prompt: "ping"}, "", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Process.CommandLine, real) {
		t.Fatalf("command %q", got.Process.CommandLine)
	}
	if !containsAll(got.Filesystem.ReadonlyPaths, binDir) {
		t.Fatalf("readonly %v", got.Filesystem.ReadonlyPaths)
	}

	shimDir := t.TempDir()
	mise := filepath.Join(shimDir, "mise")
	if err := os.WriteFile(mise, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(shimDir, "grok")
	if err := os.Symlink(mise, shim); err != nil {
		t.Fatal(err)
	}
	cfg.MXC.GrokBin = shim
	_, err := r.Run(context.Background(), cfg, chat, Request{Prompt: "ping"}, "", false)
	if err == nil || !strings.Contains(err.Error(), "not a grok binary") {
		t.Fatalf("err %v", err)
	}
}

func TestGrokBinDirWithAuthIsRefused(t *testing.T) {
	r, cfg, chat, install := testRig(t)
	if err := os.WriteFile(filepath.Join(install, "bin", "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := r.Run(context.Background(), cfg, chat, Request{Prompt: "ping"}, "", false)
	if err == nil || !strings.Contains(err.Error(), "auth.json") {
		t.Fatalf("err %v", err)
	}
}

func TestRunOmitsRulesWhenEmpty(t *testing.T) {
	r, cfg, chat, _ := testRig(t)
	got := captureReq(t, r)
	if _, err := r.Run(context.Background(), cfg, chat, Request{Prompt: "ping"}, "", false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Process.CommandLine, "--rules") {
		t.Fatalf("unexpected --rules in %q", got.Process.CommandLine)
	}
	if strings.Contains(got.Process.CommandLine, "--reasoning-effort") {
		t.Fatalf("unexpected --reasoning-effort in %q", got.Process.CommandLine)
	}
}

func TestRunEffortFlag(t *testing.T) {
	r, cfg, chat, _ := testRig(t)
	got := captureReq(t, r)
	if _, err := r.Run(context.Background(), cfg, chat, Request{Prompt: "ping", Effort: "high"}, "", false); err != nil {
		t.Fatal(err)
	}
	line := got.Process.CommandLine
	if !strings.Contains(line, "--reasoning-effort") || !strings.Contains(line, "high") {
		t.Fatalf("effort flag missing: %q", line)
	}
}

func TestRunDenyFlags(t *testing.T) {
	r, cfg, chat, _ := testRig(t)
	got := captureReq(t, r)
	req := Request{
		Prompt: "save this",
		Deny:   []string{"Read(.local/telegram-inbox/**)", "Grep(.local/telegram-inbox/**)"},
	}
	if _, err := r.Run(context.Background(), cfg, chat, req, "", false); err != nil {
		t.Fatal(err)
	}
	line := got.Process.CommandLine
	if !strings.Contains(line, "-p") || !strings.Contains(line, "save this") {
		t.Fatalf("missing -p: %q", line)
	}
	if strings.Contains(line, "--prompt-file") {
		t.Fatalf("unexpected prompt-file: %q", line)
	}
	for _, want := range []string{"--deny", "Read(.local/telegram-inbox/**)", "Grep(.local/telegram-inbox/**)"} {
		if !strings.Contains(line, want) {
			t.Fatalf("deny missing %q in %q", want, line)
		}
	}
}

func TestRunPromptFileXorPrompt(t *testing.T) {
	r, cfg, chat, _ := testRig(t)
	if _, err := r.Run(context.Background(), cfg, chat, Request{Prompt: "x", PromptFile: "turn.json"}, "", false); err == nil {
		t.Fatal("expected exclusive error")
	}
	got := captureReq(t, r)
	if _, err := r.Run(context.Background(), cfg, chat, Request{PromptFile: ".local/turn.json"}, "", false); err != nil {
		t.Fatal(err)
	}
	line := got.Process.CommandLine
	if !strings.Contains(line, "--prompt-file") || !strings.Contains(line, ".local/turn.json") {
		t.Fatalf("args %q", line)
	}
	if strings.Contains(line, "'-p'") {
		t.Fatalf("must not pass -p with prompt-file: %q", line)
	}
}

func TestRunLastTurnFromStream(t *testing.T) {
	r, cfg, chat, _ := testRig(t)
	r.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		script := `printf '%s\n' '{"type":"text","data":"I will look through the codebase."}' '{"type":"tool_call","toolCallId":"1"}' '{"type":"text","data":"Not a new alarm type."}' '{"type":"end","stopReason":"end_turn"}'`
		return exec.CommandContext(ctx, "sh", "-c", script)
	}
	res, err := r.Run(context.Background(), cfg, chat, Request{Prompt: "when was idle added"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "Not a new alarm type." {
		t.Fatalf("stdout %q", res.Stdout)
	}
	home, err := cfg.SandboxHome(chat)
	if err != nil {
		t.Fatal(err)
	}
	if res.Sandbox != home {
		t.Fatalf("sandbox %q", res.Sandbox)
	}
}

func TestReadAuthFile(t *testing.T) {
	r, cfg, chat, _ := testRig(t)
	path, err := cfg.AuthFile(chat)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"https://auth.x.ai::x":{"key":"tok-1"}}`)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadAuthFile(context.Background(), cfg, chat)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("stdout %q", got)
	}
	cfg.STT.AuthPath = filepath.Join(t.TempDir(), "auth.json")
	if _, err := r.ReadAuthFile(context.Background(), cfg, chat); err == nil {
		t.Fatal("path outside the sandbox home should fail")
	}
	cfg.STT.AuthPath = "relative"
	if _, err := r.ReadAuthFile(context.Background(), cfg, chat); err == nil {
		t.Fatal("relative path should fail")
	}
}

func TestRefreshGrokAuthRunsModels(t *testing.T) {
	r, cfg, chat, install := testRig(t)
	got := captureReq(t, r)
	if err := r.RefreshGrokAuth(context.Background(), cfg, chat); err != nil {
		t.Fatal(err)
	}
	line := got.Process.CommandLine
	if !strings.Contains(line, filepath.Join(install, "bin", "grok")) || !strings.Contains(line, "models") {
		t.Fatalf("command %q", line)
	}
	if strings.Contains(line, "cat") || strings.Contains(line, "-p") {
		t.Fatalf("refresh must not cat or prompt: %q", line)
	}
}

func containsAll(got []string, want ...string) bool {
	for _, w := range want {
		ok := false
		for _, g := range got {
			if g == w {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}
