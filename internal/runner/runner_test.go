package runner

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"telegram-jailbee/internal/config"
)

func TestRunBuildsJailbeeExec(t *testing.T) {
	r := New()
	var gotName string
	var gotArgs []string
	r.LookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	r.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		gotName, gotArgs = name, args
		cmd := exec.CommandContext(ctx, "true")
		return cmd
	}
	cfg := &config.Config{
		Grok: config.Grok{
			AlwaysApprove:   true,
			DisallowedTools: []string{"web_fetch"},
			Timeout:         "1m",
		},
		Jailbee: config.Jailbee{Binary: "jailbee"},
	}
	chat := config.Chat{
		Workspace:        "/tmp/notes",
		JailbeeContainer: "main",
	}
	if _, err := r.Run(context.Background(), cfg, chat, "ping", "11111111-1111-1111-1111-111111111111", true); err != nil {
		t.Fatal(err)
	}
	if gotName != "/usr/bin/jailbee" {
		t.Fatalf("bin %s", gotName)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{
		"-c /tmp/notes/.jailbee/config.yaml",
		"exec main --",
		"grok -p ping",
		"--always-approve",
		"--disallowed-tools web_fetch",
		"-r 11111111-1111-1111-1111-111111111111",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
}
