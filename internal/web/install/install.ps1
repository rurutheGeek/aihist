# aihist を入れて、このマシンの会話を送る。
#   $env:AIHIST_TOKEN="<トークン>"; irm {{SERVER}}/api/install.ps1 | iex
# 入れ先は %LOCALAPPDATA%\aihist\aihist.exe。$env:AIHIST_EVERY="10m" で送り続ける。
$ErrorActionPreference = 'Stop'
$server = '{{SERVER}}'
if (-not $env:AIHIST_TOKEN) { throw 'AIHIST_TOKEN が要る' }
$dir = Join-Path $env:LOCALAPPDATA 'aihist'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$exe = Join-Path $dir 'aihist.exe'
Invoke-WebRequest -UseBasicParsing -Headers @{ Authorization = "Bearer $env:AIHIST_TOKEN" } -Uri "$server/api/bin/aihist-windows-amd64.exe" -OutFile "$exe.new"
Move-Item -Force "$exe.new" $exe
$host_ = if ($env:AIHIST_HOST) { $env:AIHIST_HOST } else { $env:COMPUTERNAME.ToLower() }
$args_ = @('push', '-server', $server, '-host', $host_)
if ($env:AIHIST_EVERY) { $args_ += @('-every', $env:AIHIST_EVERY) }
& $exe @args_
