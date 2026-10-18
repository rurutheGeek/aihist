# aihist を入れて、このマシンの会話を送る。常駐はせず、タスク スケジューラが1分ごとに起こし、
# サーバーの設定の画面で決めた時刻のときだけ送る（それ以外はすぐ終わる）。
# コマンド プロンプトでも PowerShell でも次の1行で入る:
#   powershell -NoProfile -ExecutionPolicy Bypass -Command "irm {{SERVER}}/api/install.ps1 | iex; Install-Aihist -Token <トークン> -Name <マシン名>"
# 前の形（PowerShell だけ）も使える: $env:AIHIST_TOKEN="<トークン>"; irm {{SERVER}}/api/install.ps1 | iex
# $env:AIHIST_EVERY  0 なら今1回送るだけで、タスクを置かない
# $env:AIHIST_HOST   このマシンの名前（既定はコンピューター名）
# $env:AIHIST_TOOLS  送る AI（claude_code,codex,antigravity,opencode をカンマ区切り。無ければ見つかったものから選ばせる）
# $env:AIHIST_ATTACHMENTS 0 なら添付ファイル（画像など）を送らない（無ければ聞く）
# 消すとき: powershell -NoProfile -ExecutionPolicy Bypass -Command "irm {{SERVER}}/api/uninstall.ps1 | iex"
function Install-Aihist {
  param([string]$Token = $env:AIHIST_TOKEN, [string]$Name = $env:AIHIST_HOST)
  $ErrorActionPreference = 'Stop'
  $server = '{{SERVER}}'
  $ja = (Get-UICulture).TwoLetterISOLanguageName -eq 'ja'
  function M($en, $j) { if ($ja) { $j } else { $en } }
  if (-not $Token) { throw (M 'A token is required (-Token)' 'トークンが要る（-Token）') }
  $timer = $env:AIHIST_EVERY -ne '0'
  $dir = Join-Path $env:LOCALAPPDATA 'aihist'
  $conf = Join-Path $env:APPDATA 'aihist'
  New-Item -ItemType Directory -Force -Path $dir, $conf | Out-Null
  $exe = Join-Path $dir 'aihist.exe'
  Write-Host (M 'Downloading aihist...' 'aihist をダウンロード中...')
  Invoke-WebRequest -UseBasicParsing -Headers @{ Authorization = "Bearer $Token" } -Uri "$server/api/bin/aihist-windows-amd64.exe" -OutFile "$exe.new"
  Move-Item -Force "$exe.new" $exe
  $host_ = if ($Name) { $Name } else { $env:COMPUTERNAME.ToLower() }
  # 送る AI を選ぶ（Enter は見つかったもの全部）
  $tools = $env:AIHIST_TOOLS
  if ($null -eq $env:AIHIST_TOOLS) {
    $found = @(& $exe detect | Where-Object { $_ } | ForEach-Object { $f = $_ -split "`t"; [pscustomobject]@{ Tool = $f[0]; Files = [int]$f[1]; Bytes = [long]$f[2] } })
    if ($found.Count -gt 0) {
      $labels = @{ claude_code = 'Claude Code'; codex = 'Codex'; antigravity = 'Antigravity'; opencode = 'OpenCode' }
      Write-Host (M 'AI histories on this machine:' 'このマシンにある AI の記録:')
      for ($i = 0; $i -lt $found.Count; $i++) {
        $l = if ($labels[$found[$i].Tool]) { $labels[$found[$i].Tool] } else { $found[$i].Tool }
        Write-Host ('  {0}) {1,-12} {2,6} {3} {4,8:N1} MB' -f ($i + 1), $l, $found[$i].Files, (M 'files' 'ファイル'), ($found[$i].Bytes / 1MB))
      }
      $ans = Read-Host (M 'Which to send (e.g. 1,3; Enter for all)' '送る番号（例 1,3。Enter で全部）')
      if ($ans) {
        $tools = (($ans -split ',') | ForEach-Object { $found[[int]$_.Trim() - 1].Tool }) -join ','
      }
    }
  }
  # 添付ファイル（画像など）も送るか（Enter は送る）
  $attach = if ($env:AIHIST_ATTACHMENTS) { $env:AIHIST_ATTACHMENTS } else { '1' }
  if ($null -eq $env:AIHIST_ATTACHMENTS) {
    $ans = Read-Host (M 'Send attachments (images, etc.) too? [Y/n]' '添付ファイル（画像など）も送る？ [Y/n]')
    if ($ans -match '^[nN]') { $attach = '0' }
  }
  $lang = if ($ja) { 'ja' } else { 'en' }
  $cfg = Join-Path $conf 'config'
  Set-Content -Encoding ascii -Path $cfg -Value @("AIHIST_SERVER=$server", "AIHIST_TOKEN=$Token", "AIHIST_HOST=$host_", "AIHIST_LANG=$lang")
  # 設定にはトークンが入るので、自分だけが読めるようにする
  icacls $cfg /inheritance:r /grant:r "$($env:USERNAME):(R,W)" | Out-Null
  Invoke-WebRequest -UseBasicParsing -Uri "$server/api/uninstall.ps1" -OutFile (Join-Path $dir 'uninstall.ps1')

  if ($timer) {
    # conhost --headless で、動くたびに窓が出ないようにする
    $action = New-ScheduledTaskAction -Execute 'conhost.exe' -Argument "--headless `"$exe`" push -tick"
    $trigger = New-ScheduledTaskTrigger -Once -At (Get-Date).AddMinutes(1) -RepetitionInterval (New-TimeSpan -Minutes 1)
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable `
      -MultipleInstances IgnoreNew -ExecutionTimeLimit (New-TimeSpan -Hours 1) -Priority 7
    Register-ScheduledTask -TaskName 'aihist-push' -Action $action -Trigger $trigger -Settings $settings -Force | Out-Null
  }
  Write-Host (M "Installed as $host_. Sending now..." "$host_ としてインストールした。今から送る...")
  $tl = if ($tools) { $tools } else { '*' }
  $env:AIHIST_LANG = $lang
  & $exe push -init-tools $tl -init-attachments $attach
  if ($timer) {
    Write-Host (M 'Done. aihist will keep sending on the schedule set in Settings on the server.' '完了。この後はサーバーの設定の送信タイミングで自動的に送る。')
  } else {
    Write-Host (M 'Done.' '完了。')
  }
}
if ($env:AIHIST_TOKEN) { Install-Aihist -Token $env:AIHIST_TOKEN }
