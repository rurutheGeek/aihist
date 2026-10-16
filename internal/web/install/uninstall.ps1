# aihist の送信を止めて、導入スクリプトが置いたものをすべて消す（送った会話はサーバーに残る）。
#   irm {{SERVER}}/api/uninstall.ps1 | iex
Unregister-ScheduledTask -TaskName 'aihist-push' -Confirm:$false -ErrorAction SilentlyContinue
Remove-Item -Recurse -Force -ErrorAction SilentlyContinue (Join-Path $env:APPDATA 'aihist'), (Join-Path $env:LOCALAPPDATA 'aihist')
Write-Host 'aihist を消した'
