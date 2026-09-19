<#
KeywordHunter — Windows başlatıcı (PowerShell 5.1+ / 7)
  .\run.ps1            derle + çalıştır
  .\run.ps1 build      yalnızca derle (bin\keywordhunter.exe)
  .\run.ps1 test       testler
  .\run.ps1 docker     docker compose up -d --build
İlk kullanımda gerekirse:  Set-ExecutionPolicy -Scope CurrentUser RemoteSigned
#>
param([ValidateSet('run','build','test','docker')][string]$Cmd = 'run')
$ErrorActionPreference = 'Stop'
Set-Location -Path $PSScriptRoot

function Need-Go {
  if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host "[HATA] Go bulunamadı. Kurulum: https://go.dev/dl/  (veya: winget install GoLang.Go)" -ForegroundColor Red; exit 1
  }
}
function Ensure-Env {
  if (-not (Test-Path .env)) { Copy-Item .env.example .env; Write-Host "[*] .env oluşturuldu — ADMIN_PASS değerini değiştirin." -ForegroundColor Yellow }
}
function Check-Tor {
  $line = (Get-Content .env -ErrorAction SilentlyContinue | Where-Object { $_ -match '^TOR_PROXY=' } | Select-Object -First 1)
  $proxy = if ($line) { ($line -replace '^TOR_PROXY=','').Trim('"') } else { '127.0.0.1:9150' }
  $parts = $proxy.Split(':'); $h = $parts[0]; $p = [int]$parts[1]
  try { $c = New-Object Net.Sockets.TcpClient; $c.Connect($h, $p); $c.Close() }
  catch { Write-Host "[UYARI] Tor $proxy yanıt vermiyor. Tor Browser'ı açın (9150) veya Tor Expert Bundle'ı çalıştırın (9050)." -ForegroundColor Yellow }
}

switch ($Cmd) {
  'build'  { Need-Go; New-Item -ItemType Directory -Force bin | Out-Null; go build -trimpath -ldflags="-s -w" -o bin\keywordhunter.exe .\cmd; if ($LASTEXITCODE) { exit 1 }; Write-Host "[+] bin\keywordhunter.exe" }
  'test'   { Need-Go; go vet ./...; go test -race ./... }
  'docker' { New-Item -ItemType Directory -Force data | Out-Null; docker compose up -d --build; Write-Host "[+] http://localhost:8080  — ilk parola: docker compose logs app | Select-String Parola" }
  default  { Need-Go; Ensure-Env; Check-Tor; New-Item -ItemType Directory -Force bin | Out-Null
             go build -trimpath -o bin\keywordhunter.exe .\cmd; if ($LASTEXITCODE) { exit 1 }
             Write-Host "[*] Başlatılıyor → http://localhost:8080  (durdurmak için Ctrl+C)"; & .\bin\keywordhunter.exe }
}
