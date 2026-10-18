#!/bin/sh
# aihist の送信を止めて、導入スクリプトが置いたものをすべて消す（送った会話はサーバーに残る）。
#   curl -fsSL {{SERVER}}/api/uninstall.sh | sh
set -u
case "$(uname -s)" in
  Darwin) conf="$HOME/Library/Application Support/aihist" ;;
  *) conf="${XDG_CONFIG_HOME:-$HOME/.config}/aihist" ;;
esac
case "${LC_ALL:-${LC_MESSAGES:-${LANG:-}}}" in ja*) lang=ja ;; *) lang=en ;; esac
list="$conf/installed"
if [ ! -f "$list" ]; then
  if [ "$lang" = ja ]; then echo "aihist は入っていない（$list が無い）"; else echo "aihist isn't installed ($list not found)"; fi
  exit 0
fi
while read -r kind path; do
  case "$kind" in
    launchd)
      launchctl bootout "gui/$(id -u)/aihist.push" 2>/dev/null
      rm -f "$path" ;;
    systemd)
      case "$path" in *.timer) systemctl --user disable --now aihist-push.timer 2>/dev/null ;; esac
      rm -f "$path" ;;
    cron)
      crontab -l 2>/dev/null | grep -v '# aihist-push$' | crontab - ;;
    linger)
      loginctl disable-linger 2>/dev/null ;;
    *)
      rm -f "$kind" ;;
  esac
done < "$list"
systemctl --user daemon-reload 2>/dev/null
rm -rf "$conf"
if [ "$lang" = ja ]; then echo "aihist をアンインストールした。送った会話はサーバーに残っている。"; else echo "Uninstalled aihist. Chats already sent stay on the server."; fi
