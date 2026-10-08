package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeResolvFixture lays out a systemd-resolved stub symlink. uplink is
// skipped when empty.
func writeResolvFixture(t *testing.T, uplink string) (link, dir string) {
	t.Helper()
	root := t.TempDir()
	dir = filepath.Join(root, "run", "systemd", "resolve")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stub-resolv.conf"), []byte("nameserver 127.0.0.53\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if uplink != "" {
		if err := os.WriteFile(filepath.Join(dir, "resolv.conf"), []byte(uplink), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link = filepath.Join(root, "resolv.conf")
	if err := os.Symlink(filepath.Join(dir, "stub-resolv.conf"), link); err != nil {
		t.Fatal(err)
	}
	return link, dir
}

func TestStubResolverWrapsUnshare(t *testing.T) {
	r, cfg, chat, _ := testRig(t)
	link, dir := writeResolvFixture(t, "nameserver 10.9.8.7\n")
	r.resolvFile = link
	var name string
	var args []string
	r.Command = func(ctx context.Context, bin string, argv ...string) *exec.Cmd {
		name = bin
		args = append([]string(nil), argv...)
		return exec.CommandContext(ctx, "true")
	}
	if _, err := r.Run(context.Background(), cfg, chat, Request{Prompt: "ping"}, "", false); err != nil {
		t.Fatal(err)
	}
	if name != "/usr/bin/unshare" {
		t.Fatalf("bin %s", name)
	}
	joined := strings.Join(args, "\n")
	for _, want := range []string{"--user", "--map-root-user", "--mount", "--propagation", "private", "mount --bind"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %q:\n%s", want, joined)
		}
	}
	src, dst, bin := args[len(args)-4], args[len(args)-3], args[len(args)-2]
	if src != filepath.Join(dir, "resolv.conf") || dst != filepath.Join(dir, "stub-resolv.conf") {
		t.Fatalf("bind %s -> %s", src, dst)
	}
	if bin != "/usr/bin/lxc-exec" {
		t.Fatalf("lxc-exec %s", bin)
	}
}

func TestPlainResolverRunsLxcExecDirectly(t *testing.T) {
	r := New()
	path := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.WriteFile(path, []byte("nameserver 9.9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.resolvFile = path
	_, _, ok, err := r.sandboxResolv()
	if err != nil || ok {
		t.Fatalf("ok %v err %v", ok, err)
	}
}

func TestStubResolverNeedsUplinkFile(t *testing.T) {
	r := New()
	r.resolvFile, _ = writeResolvFixture(t, "")
	if _, _, _, err := r.sandboxResolv(); err == nil {
		t.Fatal("want error when resolv.conf is missing next to the stub")
	}
}

func TestLiveSandboxDNS(t *testing.T) {
	if os.Getenv("KERFLINE_DNS_LIVE") == "" {
		t.Skip("set KERFLINE_DNS_LIVE=1 to probe sandbox DNS")
	}
	bin, err := exec.LookPath("lxc-exec")
	if err != nil {
		t.Skip(err)
	}
	src, dst, ok, err := New().sandboxResolv()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Skip("this host's resolv.conf is not the systemd-resolved stub")
	}
	work := t.TempDir()
	probe := filepath.Join(work, "probe.py")
	if err := os.WriteFile(probe, []byte(`import socket
s = socket.create_connection(("cli-chat-proxy.grok.com", 443), 8)
print("tcp", s.getpeername())
s.close()
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(work, "mxc.json")
	raw := `{
  "version": "1.0.0",
  "containment": "bubblewrap",
  "process": {
    "commandLine": "python3 ` + probe + `",
    "cwd": "/tmp",
    "inheritDefaultEnv": true,
    "timeout": 20000
  },
  "filesystem": {"readwritePaths": ["/tmp"], "readonlyPaths": ["` + work + `"]},
  "network": {
    "egress": {"default": "allow"},
    "ingress": {"default": "deny", "hostLoopback": "deny"}
  }
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("unshare",
		"--user", "--map-root-user", "--mount", "--propagation", "private",
		"--", "sh", "-c", sandboxResolvScript,
		"sh", src, dst, bin, cfgPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sandbox dns: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "tcp") {
		t.Fatalf("output %s", out)
	}
}
