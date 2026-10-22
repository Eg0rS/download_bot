#!/bin/sh
set -eu
case "${SSH_ORIGINAL_COMMAND:-}" in
  deploy\ *) revision=${SSH_ORIGINAL_COMMAND#deploy } ;;
  *) echo "Only deploy <commit> is allowed" >&2; exit 1 ;;
esac
case "$revision" in ""|*[!0-9a-f]*) exit 1 ;; esac
[ "${#revision}" -eq 40 ] || exit 1
exec /opt/media-downloader/deploy.sh "$revision"
