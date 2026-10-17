#!/bin/sh
# aihist を入れて、このマシンの会話を送る。常駐はせず、タイマーが1分ごとに起こし、
# サーバーの設定の画面で決めた時刻のときだけ送る（それ以外はすぐ終わる）。
#   curl -fsSL {{SERVER}}/api/install.sh | AIHIST_TOKEN=<トークン> sh
# AIHIST_EVERY  0 なら今1回送るだけで、タイマーを置かない
# AIHIST_HOST   このマシンの名前（既定はホスト名。WSL では -wsl を付ける）
# AIHIST_BIN_DIR 実行ファイルの置き場所（既定 ~/.local/bin）
# AIHIST_TOOLS  送る AI（claude_code,codex,antigravity,opencode をカンマ区切り。無ければ見つかったものから選ばせる）
# AIHIST_ATTACHMENTS 0 なら添付ファイル（画像など）を送らない（無ければ聞く）
# 送る AI と添付ファイルは、サーバーのこのマシンの設定の最初の値になる（あとは設定の画面で変える）
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
timer=1
[ "${AIHIST_EVERY:-}" = 0 ] && timer=0
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
# 添付ファイル（画像など）も送るか（Enter は送る）
attach="${AIHIST_ATTACHMENTS:-1}"
if [ -z "${AIHIST_ATTACHMENTS+x}" ] && [ -r /dev/tty ]; then
  printf '添付ファイル（画像など）も送る？ [Y/n]: '
  read -r ans < /dev/tty || ans=""
  case "$ans" in n* | N*) attach=0 ;; esac
fi
(
  umask 077
  printf 'AIHIST_SERVER=%s\nAIHIST_TOKEN=%s\nAIHIST_HOST=%s\n' "$server" "$AIHIST_TOKEN" "$host" > "$conf/config"
)
curl -fsSL -o "$conf/uninstall.sh" "$server/api/uninstall.sh"
chmod +x "$conf/uninstall.sh"
# 消すときに残さないよう、置いたものを書き留める
: > "$conf/installed"
echo "$bin" >> "$conf/installed"

if [ "$timer" = 1 ]; then
  if [ "$os" = darwin ]; then
    plist="$HOME/Library/LaunchAgents/aihist.push.plist"
    mkdir -p "$HOME/Library/LaunchAgents"
    cat > "$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>aihist.push</string>
  <key>ProgramArguments</key><array><string>$bin</string><string>push</string><string>-tick</string></array>
  <key>StartInterval</key><integer>60</integer>
  <key>RunAtLoad</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>LowPriorityIO</key><true/>
  <key>Nice</key><integer>10</integer>
</dict></plist>
PLIST
    launchctl bootout "gui/$(id -u)/aihist.push" 2>/dev/null || true
    launchctl bootstrap "gui/$(id -u)" "$plist"
    echo "launchd $plist" >> "$conf/installed"
    echo "launchd に置いた（送る時刻はサーバーの設定の画面で決める）"
  elif systemctl --user show-environment >/dev/null 2>&1; then
    units="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
    mkdir -p "$units"
    cat > "$units/aihist-push.service" <<UNIT
[Unit]
Description=aihist: 会話を送る

[Service]
Type=oneshot
ExecStart=$bin push -tick
Nice=10
IOSchedulingClass=idle
UNIT
    cat > "$units/aihist-push.timer" <<UNIT
[Unit]
Description=aihist: 送る時刻か確かめる

[Timer]
OnCalendar=minutely

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
    echo "systemd のタイマーに置いた（送る時刻はサーバーの設定の画面で決める）"
  elif command -v crontab >/dev/null 2>&1; then
    sched="* * * * *"
    cmd="$bin push -tick"
    command -v flock >/dev/null 2>&1 && cmd="flock -n '$conf/lock' $cmd"
    { crontab -l 2>/dev/null | grep -v '# aihist-push$' || true; echo "$sched $cmd >/dev/null 2>&1 # aihist-push"; } | crontab -
    echo "cron" >> "$conf/installed"
    echo "cron に置いた（送る時刻はサーバーの設定の画面で決める）"
  else
    echo "タイマーを置けない（systemd --user も cron も無い）。今1回だけ送る" >&2
  fi
fi
"$bin" push -init-tools "${tools:-*}" -init-attachments "$attach"
