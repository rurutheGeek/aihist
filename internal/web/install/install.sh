#!/bin/sh
# aihist を入れて、このマシンの会話を送る。
#   curl -fsSL {{SERVER}}/api/install.sh | AIHIST_TOKEN=<トークン> sh
# 入れ先は ~/.local/bin/aihist。AIHIST_EVERY=10m で送り続ける（前面で動く）。
set -eu
server='{{SERVER}}'
: "${AIHIST_TOKEN:?AIHIST_TOKEN が要る}"
case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "対応していない OS: $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "対応していない CPU: $(uname -m)" >&2; exit 1 ;;
esac
dir="${AIHIST_BIN_DIR:-$HOME/.local/bin}"
mkdir -p "$dir"
tmp="$dir/.aihist.$$"
curl -fsSL -H "Authorization: Bearer $AIHIST_TOKEN" -o "$tmp" "$server/api/bin/aihist-$os-$arch"
chmod +x "$tmp"
mv "$tmp" "$dir/aihist"
host="${AIHIST_HOST:-$(hostname)}"
if grep -qi microsoft /proc/version 2>/dev/null && [ -z "${AIHIST_HOST:-}" ]; then
  host="$host-wsl"
fi
set -- push -server "$server" -host "$host"
[ -n "${AIHIST_EVERY:-}" ] && set -- "$@" -every "$AIHIST_EVERY"
AIHIST_TOKEN="$AIHIST_TOKEN" exec "$dir/aihist" "$@"
