$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Push-Location (Join-Path $root 'web')
try {
  npm ci
  npm run check
  npm run build
} finally { Pop-Location }

Push-Location $root
try {
  go test ./...
  New-Item -ItemType Directory -Force -Path 'dist' | Out-Null
  $env:CGO_ENABLED = '0'
  $env:GOOS = 'windows'; $env:GOARCH = 'amd64'; go build -trimpath -ldflags '-s -w' -o 'dist/pmbattle-windows-amd64.exe' .
  $env:GOOS = 'linux'; $env:GOARCH = 'amd64'; go build -trimpath -ldflags '-s -w' -o 'dist/pmbattle-linux-amd64' .
} finally { Pop-Location }

