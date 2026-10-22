#!/bin/sh
# Run as root once, after installing Docker Engine and Compose v2.
# Arguments: path to .env, path to the GitHub Actions public key.
set -eu
[ "$(id -u)" -eq 0 ] || { echo "Run as root" >&2; exit 1; }
docker info >/dev/null
docker compose version >/dev/null
command -v flock >/dev/null
command -v curl >/dev/null
env_file=${1:?Path to production .env}
public_key_file=${2:?Path to deploy SSH public key}
base=/opt/media-downloader
install -d -m 700 "$base" "$base/releases" "$base/bin" /root/.ssh
if [ ! -f "$base/.env" ]; then install -m 600 "$env_file" "$base/.env"; fi
source_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
install -m 700 "$source_dir/deploy.sh" "$base/deploy.sh"
install -m 700 "$source_dir/deploy-ssh.sh" "$base/bin/deploy-ssh"
key=$(cat "$public_key_file")
case "$key" in ssh-ed25519\ *) ;; *) echo "Expected an Ed25519 public key" >&2; exit 1 ;; esac
touch /root/.ssh/authorized_keys
chmod 600 /root/.ssh/authorized_keys
if ! grep -Fq "$key" /root/.ssh/authorized_keys; then
 printf 'restrict,command="/opt/media-downloader/bin/deploy-ssh" %s\n' "$key" >> /root/.ssh/authorized_keys
fi
echo "Server deployment entrypoint ready"
