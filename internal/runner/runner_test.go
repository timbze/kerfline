package runner

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/timbze/kerfline/internal/config"
)

func identityResolve(_ context.Context, _ *config.Config, chat config.Chat) (string, error) {
	return chat.JailbeeContainer, nil
}

func TestRunBuildsJailbeeExec(t *testing.T) {
	r := New()
	r.Resolve = identityResolve
	var gotName string
	var gotArgs []string
	var gotCmd *exec.Cmd
	r.LookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	r.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		gotName, gotArgs = name, args
		gotCmd = exec.CommandContext(ctx, "true")
		return gotCmd
	}
	cfg := &config.Config{
		Grok: config.Grok{
			AlwaysApprove:   true,
			DisallowedTools: []string{"web_fetch"},
			Timeout:         "1m",
		},
		Jailbee: config.Jailbee{Binary: "jailbee"},
	}
	ws := t.TempDir()
	chat := config.Chat{
		Workspace:        ws,
		JailbeeContainer: "main",
	}
	cfg.Rules = "Keep it short."
	if _, err := r.Run(context.Background(), cfg, chat, Request{Prompt: "ping"}, "11111111-1111-1111-1111-111111111111", true); err != nil {
		t.Fatal(err)
	}
	if gotName != "/usr/bin/jailbee" {
		t.Fatalf("bin %s", gotName)
	}
	if gotCmd.Dir != ws {
		t.Fatalf("dir %s", gotCmd.Dir)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{
		"exec -c " + ws + "/.jailbee/config.yaml main --",
		"grok -p ping",
		"--always-approve",
		"--disallowed-tools web_fetch",
		"-r 11111111-1111-1111-1111-111111111111",
		"--output-format streaming-json",
		"--rules Keep it short.",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
}

func TestRunOmitsRulesWhenEmpty(t *testing.T) {
	r := New()
	r.Resolve = identityResolve
	var gotArgs []string
	r.LookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	r.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		gotArgs = args
		return exec.CommandContext(ctx, "true")
	}
	cfg := &config.Config{Jailbee: config.Jailbee{Binary: "jailbee"}}
	chat := config.Chat{Workspace: t.TempDir(), JailbeeContainer: "main"}
	if _, err := r.Run(context.Background(), cfg, chat, Request{Prompt: "ping"}, "", false); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(gotArgs, " ")
	if strings.Contains(joined, "--rules") {
		t.Fatalf("unexpected --rules in %q", joined)
	}
}

func TestRunDenyFlags(t *testing.T) {
	r := New()
	r.Resolve = identityResolve
	var gotArgs []string
	r.LookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	r.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		gotArgs = args
		return exec.CommandContext(ctx, "true")
	}
	cfg := &config.Config{Jailbee: config.Jailbee{Binary: "jailbee"}}
	chat := config.Chat{Workspace: t.TempDir(), JailbeeContainer: "main"}
	req := Request{
		Prompt: "save this",
		Deny:   []string{"Read(.local/telegram-inbox/**)", "Grep(.local/telegram-inbox/**)"},
	}
	if _, err := r.Run(context.Background(), cfg, chat, req, "", false); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "grok -p save this") {
		t.Fatalf("missing -p: %q", joined)
	}
	if strings.Contains(joined, "--prompt-file") {
		t.Fatalf("unexpected prompt-file: %q", joined)
	}
	want := []string{"--deny", "Read(.local/telegram-inbox/**)", "--deny", "Grep(.local/telegram-inbox/**)"}
	if !containsSeq(gotArgs, want) {
		t.Fatalf("deny not separate argv entries: %q", joined)
	}
}

func TestRunPromptFileXorPrompt(t *testing.T) {
	r := New()
	r.Resolve = identityResolve
	var gotArgs []string
	r.LookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	r.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		gotArgs = args
		return exec.CommandContext(ctx, "true")
	}
	cfg := &config.Config{Jailbee: config.Jailbee{Binary: "jailbee"}}
	chat := config.Chat{Workspace: t.TempDir(), JailbeeContainer: "main"}
	if _, err := r.Run(context.Background(), cfg, chat, Request{Prompt: "x", PromptFile: "turn.json"}, "", false); err == nil {
		t.Fatal("expected exclusive error")
	}
	if _, err := r.Run(context.Background(), cfg, chat, Request{PromptFile: ".local/turn.json"}, "", false); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "--prompt-file .local/turn.json") {
		t.Fatalf("args %q", joined)
	}
	if strings.Contains(joined, "grok -p ") {
		t.Fatalf("must not pass -p with prompt-file: %q", joined)
	}
}

func TestRunLastTurnFromStream(t *testing.T) {
	r := New()
	r.Resolve = identityResolve
	r.LookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	r.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		script := `printf '%s\n' '{"type":"text","data":"I will look through the codebase."}' '{"type":"tool_call","toolCallId":"1"}' '{"type":"text","data":"Not a new alarm type."}' '{"type":"end","stopReason":"end_turn"}'`
		return exec.CommandContext(ctx, "sh", "-c", script)
	}
	cfg := &config.Config{Jailbee: config.Jailbee{Binary: "jailbee"}}
	chat := config.Chat{Workspace: t.TempDir(), JailbeeContainer: "main"}
	res, err := r.Run(context.Background(), cfg, chat, Request{Prompt: "when was idle added"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "Not a new alarm type." {
		t.Fatalf("stdout %q", res.Stdout)
	}
}

func TestReadContainerFile(t *testing.T) {
	r := New()
	r.Resolve = identityResolve
	var gotArgs []string
	body := `{"https://auth.x.ai::x":{"key":"tok-1"}}`
	r.LookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	r.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		gotArgs = args
		return exec.CommandContext(ctx, "printf", "%s", body)
	}
	cfg := &config.Config{Jailbee: config.Jailbee{Binary: "jailbee"}}
	chat := config.Chat{Workspace: t.TempDir(), JailbeeContainer: "main"}
	got, err := r.ReadContainerFile(context.Background(), cfg, chat, "/home/dev/.grok/auth.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("stdout %q", got)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{
		"exec -c " + chat.JailbeeConfig() + " main --",
		"cat -- /home/dev/.grok/auth.json",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
	if _, err := r.ReadContainerFile(context.Background(), cfg, chat, "relative"); err == nil {
		t.Fatal("relative path should fail")
	}
}

func TestRefreshGrokAuthRunsModels(t *testing.T) {
	r := New()
	r.Resolve = identityResolve
	var gotArgs []string
	r.LookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	r.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		gotArgs = args
		return exec.CommandContext(ctx, "true")
	}
	cfg := &config.Config{Jailbee: config.Jailbee{Binary: "jailbee"}}
	chat := config.Chat{Workspace: t.TempDir(), JailbeeContainer: "main"}
	if err := r.RefreshGrokAuth(context.Background(), cfg, chat); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{
		"exec -c " + chat.JailbeeConfig() + " main --",
		"grok models",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "cat --") || strings.Contains(joined, "-p ") {
		t.Fatalf("refresh must not cat or prompt: %q", joined)
	}
}

func TestPickContainerPrefersWorkspaceFullName(t *testing.T) {
	rows := []containerRow{
		{Name: "main", FullName: "gbcmensl-main", State: "Running"},
	}
	got, err := pickContainer("main", rows)
	if err != nil {
		t.Fatal(err)
	}
	if got != "gbcmensl-main" {
		t.Fatalf("got %q", got)
	}
	got, err = pickContainer("gbcmensl-main", rows)
	if err != nil {
		t.Fatal(err)
	}
	if got != "gbcmensl-main" {
		t.Fatalf("full name %q", got)
	}
}

func TestPickContainerMissing(t *testing.T) {
	_, err := pickContainer("main", []containerRow{{Name: "feat", FullName: "notes-feat"}})
	if err == nil || !strings.Contains(err.Error(), `jailbee container "main" is not in this workspace`) {
		t.Fatalf("err %v", err)
	}
}

func TestPickContainerAmbiguous(t *testing.T) {
	_, err := pickContainer("main", []containerRow{
		{Name: "main", FullName: "notes-main"},
		{Name: "main", FullName: "other-main"},
	})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("err %v", err)
	}
}

func TestRunExecsListingFullNameNotShortName(t *testing.T) {
	r := New()
	var execs [][]string
	r.LookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	r.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		execs = append(execs, append([]string{name}, args...))
		joined := strings.Join(args, " ")
		if strings.HasPrefix(joined, "ls ") {
			return exec.CommandContext(ctx, "printf", "%s", `[{"name":"main","full_name":"gbcmensl-main","state":"Running"}]`)
		}
		return exec.CommandContext(ctx, "true")
	}
	cfg := &config.Config{Jailbee: config.Jailbee{Binary: "jailbee"}}
	chat := config.Chat{Workspace: t.TempDir(), JailbeeContainer: "main"}
	res, err := r.Run(context.Background(), cfg, chat, Request{Prompt: "ping"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Container != "gbcmensl-main" {
		t.Fatalf("container %q", res.Container)
	}
	if len(execs) != 2 {
		t.Fatalf("calls %d: %v", len(execs), execs)
	}
	ls := strings.Join(execs[0], " ")
	if !strings.Contains(ls, "ls -c "+chat.JailbeeConfig()) || !strings.Contains(ls, "-o json") {
		t.Fatalf("ls args %q", ls)
	}
	run := strings.Join(execs[1], " ")
	if !strings.Contains(run, "exec -c "+chat.JailbeeConfig()+" gbcmensl-main --") {
		t.Fatalf("exec must use listing full_name, got %q", run)
	}
	if strings.Contains(run, "exec -c "+chat.JailbeeConfig()+" main --") {
		t.Fatalf("must not exec short name: %q", run)
	}
}

func TestRunCachesResolvedName(t *testing.T) {
	r := New()
	var ls int
	r.LookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	r.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		joined := strings.Join(args, " ")
		if strings.HasPrefix(joined, "ls ") {
			ls++
			return exec.CommandContext(ctx, "printf", "%s", `[{"name":"main","full_name":"notes-main","state":"Running"}]`)
		}
		return exec.CommandContext(ctx, "true")
	}
	cfg := &config.Config{Jailbee: config.Jailbee{Binary: "jailbee"}}
	chat := config.Chat{Workspace: t.TempDir(), JailbeeContainer: "main"}
	if _, err := r.Run(context.Background(), cfg, chat, Request{Prompt: "a"}, "", false); err != nil {
		t.Fatal(err)
	}
	if err := r.RefreshGrokAuth(context.Background(), cfg, chat); err != nil {
		t.Fatal(err)
	}
	if ls != 1 {
		t.Fatalf("ls ran %d times, want 1", ls)
	}
}

func TestResolveFromListMissingErrors(t *testing.T) {
	r := New()
	r.LookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	r.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "printf", "%s", `[{"name":"feat","full_name":"notes-feat","state":"Running"}]`)
	}
	cfg := &config.Config{Jailbee: config.Jailbee{Binary: "jailbee"}}
	chat := config.Chat{Workspace: t.TempDir(), JailbeeContainer: "main"}
	_, err := r.Run(context.Background(), cfg, chat, Request{Prompt: "ping"}, "", false)
	if err == nil || !strings.Contains(err.Error(), `jailbee container "main" is not in this workspace`) {
		t.Fatalf("err %v", err)
	}
}

func containsSeq(args, want []string) bool {
	if len(want) == 0 || len(args) < len(want) {
		return false
	}
	for i := 0; i+len(want) <= len(args); i++ {
		ok := true
		for j := range want {
			if args[i+j] != want[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
