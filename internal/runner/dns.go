package runner

import (
	"fmt"
	"os"
	"path/filepath"
)

func (r *Runner) resolvPath() string {
	if r != nil && r.resolvFile != "" {
		return r.resolvFile
	}
	return "/etc/resolv.conf"
}

// sandboxResolv returns the bind that gives the sandbox a resolver it can reach.
//
// Grok is a static musl binary and reads /etc/resolv.conf directly. When that
// is systemd-resolved's stub file, it lists 127.0.0.53, which is the sandbox's
// own loopback. resolved also writes the real uplink servers to resolv.conf in
// the same directory, so binding that over the stub, inside a private mount
// namespace, fixes the sandbox and leaves the host alone. slirp4netns starts in
// that namespace too and forwards to the same uplinks.
//
// ok is false when /etc/resolv.conf is not the stub and needs no change.
func (r *Runner) sandboxResolv() (src, dst string, ok bool, err error) {
	dst, err = filepath.EvalSymlinks(r.resolvPath())
	if err != nil {
		return "", "", false, fmt.Errorf("sandbox DNS: %w", err)
	}
	if filepath.Base(dst) != "stub-resolv.conf" {
		return "", "", false, nil
	}
	src = filepath.Join(filepath.Dir(dst), "resolv.conf")
	if _, err := os.Stat(src); err != nil {
		return "", "", false, fmt.Errorf("sandbox DNS: %s points at the loopback stub and %w", r.resolvPath(), err)
	}
	return src, dst, true, nil
}

const sandboxResolvScript = `set -eu
mount --bind "$1" "$2"
exec "$3" --config "$4"
`
