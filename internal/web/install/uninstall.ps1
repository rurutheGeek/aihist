# aihist の送信を止めて、導入スクリプトが置いたものをすべて消す（送った会話はサーバーに残る）。
#   powershell -NoProfile -ExecutionPolicy Bypass -Command "irm {{SERVER}}/api/uninstall.ps1 | iex"
Unregister-ScheduledTask -TaskName 'aihist-push' -Confirm:$false -ErrorAction SilentlyContinue
Remove-Item -Recurse -Force -ErrorAction SilentlyContinue (Join-Path $env:APPDATA 'aihist'), (Join-Path $env:LOCALAPPDATA 'aihist')
if ((Get-UICulture).TwoLetterISOLanguageName -eq 'ja') { Write-Host 'aihist をアンインストールした。送った会話はサーバーに残っている。' } else { Write-Host 'Uninstalled aihist. Chats already sent stay on the server.' }
