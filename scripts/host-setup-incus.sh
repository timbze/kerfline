#!/usr/bin/env bash
# One-time Arch host setup for JailBee. Run as: sudo -E ./scripts/host-setup-incus.sh
set -euo pipefail

if [[ ${EUID} -ne 0 ]]; then
  echo "run as root: sudo -E $0" >&2
  exit 1
fi

USER_NAME=${SUDO_USER:-${USER}}
USER_UID=$(id -u "${USER_NAME}")
USER_GID=$(id -g "${USER_NAME}")

pacman -S --needed --noconfirm incus

usermod -aG incus,incus-admin "${USER_NAME}"

# Incus unprivileged range (root daemon).
grep -qxF "root:1000000:1000000000" /etc/subuid \
  || echo "root:1000000:1000000000" >> /etc/subuid
grep -qxF "root:1000000:1000000000" /etc/subgid \
  || echo "root:1000000:1000000000" >> /etc/subgid
# JailBee identity map for host file mounts.
grep -qxF "root:${USER_UID}:1" /etc/subuid \
  || echo "root:${USER_UID}:1" >> /etc/subuid
grep -qxF "root:${USER_GID}:1" /etc/subgid \
  || echo "root:${USER_GID}:1" >> /etc/subgid

systemctl enable --now incus.service

if command -v ufw >/dev/null && ufw status | grep -q "Status: active"; then
  ufw route allow in on incusbr0 || true
  ufw route allow in on jailbee-loose || true
  echo "UFW is active. If containers get no DHCP, add the before.rules lines from JailBee docs."
fi

echo
echo "Done. Log out and back in so group incus-admin applies, then:"
echo "  incus admin init     # accept defaults if this is a fresh Incus"
echo "  jailbee setup --yes"
echo "  jailbee doctor"
echo "  cd ~/kerfline-workspaces/notes && jailbee init && jailbee base build && jailbee new main"
