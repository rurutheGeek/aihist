#!/bin/sh
# aihist を入れて、このマシンの会話を定期的に送る（常駐はせず、タイマーが一定間隔で1回ずつ動かす）。
#   curl -fsSL {{SERVER}}/api/install.sh | AIHIST_TOKEN=<トークン> sh
# AIHIST_EVERY  送る間隔（既定 10m。s・m・h。0 なら今1回送るだけでタイマーを置かない）
# AIHIST_HOST   このマシンの名前（既定はホスト名。WSL では -wsl を付ける）
# AIHIST_BIN_DIR 実行ファイルの置き場所（既定 ~/.local/bin）
# AIHIST_IMAGES 0 なら埋め込まれた画像を抜いて送る（無ければ聞く）
# AIHIST_TOOLS  送る AI（claude_code,codex,antigravity,opencode をカンマ区切り。無ければ見つかったものから選ばせる）
# 消すとき: curl -fsSL {{SERVER}}/api/uninstall.sh | sh （手元の ~/.config/aihist/uninstall.sh でもよい）
set -eu
server='{{SERVER}}'
: "${AIHIST_TOKEN:?AIHIST_TOKEN が要る}"
case "$(uname -s)" in
  Linux) os=linux; conf="${XDG_CONFIG_HOME:-$HOME/.config}/aihist" ;;
  Darwin) os=darwin; conf="$HOME/Library/Application Support/aihist" ;;
  *) echo "対応していない OS: $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "対応していない CPU: $(uname -m)" >&2; exit 1 ;;
esac
every="${AIHIST_EVERY:-10m}"
case "$every" in
  0) secs=0 ;;
  *s) secs=${every%s} ;;
  *m) secs=$((${every%m} * 60)) ;;
  *h) secs=$((${every%h} * 3600)) ;;
  *) echo "AIHIST_EVERY は 30s・10m・1h の形で" >&2; exit 1 ;;
esac
bindir="${AIHIST_BIN_DIR:-$HOME/.local/bin}"
bin="$bindir/aihist"
host="${AIHIST_HOST:-$(hostname)}"
if grep -qi microsoft /proc/version 2>/dev/null && [ -z "${AIHIST_HOST:-}" ]; then
  host="$host-wsl"
fi

mkdir -p "$bindir" "$conf"
tmp="$bindir/.aihist.$$"
curl -fsSL -H "Authorization: Bearer $AIHIST_TOKEN" -o "$tmp" "$server/api/bin/aihist-$os-$arch"
chmod +x "$tmp"
mv "$tmp" "$bin"
# 送る AI を選ぶ（端末があるときだけ聞く。Enter は見つかったもの全部）
tools="${AIHIST_TOOLS-}"
if [ -z "${AIHIST_TOOLS+x}" ] && [ -r /dev/tty ] && found=$("$bin" detect) && [ -n "$found" ]; then
  echo "このマシンにある AI の記録:"
  echo "$found" | awk -F'\t' '{
    l = $1
    if (l == "claude_code") l = "Claude Code"; else if (l == "codex") l = "Codex"; else if (l == "antigravity") l = "Antigravity"; else if (l == "opencode") l = "OpenCode"
    b = $3; split("B KB MB GB TB", u, " "); i = 1; while (b >= 1024 && i < 5) { b /= 1024; i++ }
    printf "  %d) %-12s %6d ファイル %8.1f %s\n", NR, l, $2, b, u[i]
  }'
  printf '送る番号（例 1,3。Enter で全部）: '
  read -r ans < /dev/tty || ans=""
  if [ -n "$ans" ]; then
    tools=$(echo "$found" | awk -F'\t' -v pick=",$(echo "$ans" | tr -d ' ')," 'index(pick, "," NR ",") { printf "%s%s", sep, $1; sep = "," }')
  fi
fi
# 埋め込まれた画像も送るか（Enter は送る）
images="${AIHIST_IMAGES:-1}"
if [ -z "${AIHIST_IMAGES+x}" ] && [ -r /dev/tty ]; then
  printf '画像も送る？ [Y/n]: '
  read -r ans < /dev/tty || ans=""
  case "$ans" in n* | N*) images=0 ;; esac
fi
(
  umask 077
  printf 'AIHIST_SERVER=%s\nAIHIST_TOKEN=%s\nAIHIST_HOST=%s\nAIHIST_TOOLS=%s\nAIHIST_IMAGES=%s\n' "$server" "$AIHIST_TOKEN" "$host" "$tools" "$images" > "$conf/config"
)
[ "$images" = 0 ] && echo "画像は送らない"
[ -n "$tools" ] && echo "送る AI: $tools"
curl -fsSL -o "$conf/uninstall.sh" "$server/api/uninstall.sh"
chmod +x "$conf/uninstall.sh"
# 消すときに残さないよう、置いたものを書き留める
: > "$conf/installed"
echo "$bin" >> "$conf/installed"

if [ "$secs" -gt 0 ]; then
  if [ "$os" = darwin ]; then
    plist="$HOME/Library/LaunchAgents/aihist.push.plist"
    mkdir -p "$HOME/Library/LaunchAgents"
    cat > "$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>aihist.push</string>
  <key>ProgramArguments</key><array><string>$bin</string><string>push</string></array>
  <key>StartInterval</key><integer>$secs</integer>
  <key>RunAtLoad</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>LowPriorityIO</key><true/>
  <key>Nice</key><integer>10</integer>
</dict></plist>
PLIST
    launchctl bootout "gui/$(id -u)/aihist.push" 2>/dev/null || true
    launchctl bootstrap "gui/$(id -u)" "$plist"
    echo "launchd $plist" >> "$conf/installed"
    echo "launchd で $every ごとに送る"
  elif systemctl --user show-environment >/dev/null 2>&1; then
    units="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
    mkdir -p "$units"
    cat > "$units/aihist-push.service" <<UNIT
[Unit]
Description=aihist: 会話を送る

[Service]
Type=oneshot
ExecStart=$bin push
Nice=10
IOSchedulingClass=idle
UNIT
    cat > "$units/aihist-push.timer" <<UNIT
[Unit]
Description=aihist: $every ごとに会話を送る

[Timer]
OnStartupSec=1min
OnUnitActiveSec=$secs
Persistent=true

[Install]
WantedBy=timers.target
UNIT
    systemctl --user daemon-reload
    systemctl --user enable --now aihist-push.timer >/dev/null
    echo "systemd $units/aihist-push.service" >> "$conf/installed"
    echo "systemd $units/aihist-push.timer" >> "$conf/installed"
    # ログインしていない間も送るには linger が要る（自分で入れたときだけ、消すときに戻す）
    if command -v loginctl >/dev/null 2>&1 && [ "$(loginctl show-user "$(id -un)" -p Linger --value 2>/dev/null)" = no ]; then
      loginctl enable-linger 2>/dev/null && echo "linger" >> "$conf/installed" || true
    fi
    echo "systemd のタイマーで $every ごとに送る"
  elif command -v crontab >/dev/null 2>&1; then
    m=$(( (secs + 59) / 60 ))
    if [ "$m" -ge 60 ]; then sched="0 */$((m / 60)) * * *"; else sched="*/$m * * * *"; fi
    cmd="$bin push"
    command -v flock >/dev/null 2>&1 && cmd="flock -n '$conf/lock' $cmd"
    { crontab -l 2>/dev/null | grep -v '# aihist-push$' || true; echo "$sched $cmd >/dev/null 2>&1 # aihist-push"; } | crontab -
    echo "cron" >> "$conf/installed"
    echo "cron で $every ごとに送る"
  else
    echo "タイマーを置けない（systemd --user も cron も無い）。今1回だけ送る" >&2
  fi
fi
"$bin" push
