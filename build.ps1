<#
CliSimpleHub build script (PowerShell, Windows-friendly).

Examples:
  .\build.ps1                                        # desktop current
  .\build.ps1 -Command linux                         # desktop linux + desktop linux-proxy
  .\build.ps1 -Target server -Command current        # server current + server current-proxy
  .\build.ps1 -Target both -Platform linux/amd64 -Platform windows/amd64
#>

[CmdletBinding(PositionalBinding=$false)]
param(
  [Parameter(Position=0)]
  [ValidateSet('desktop','server','both')]
  [string]$Target = 'desktop',

  [Parameter(Position=1)]
  [ValidateSet('current','macos','linux','windows','all','clean')]
  [string]$Command = 'current',

  [Alias('v')]
  [string]$Version = $env:VERSION,

  [string]$Tags = '',

  [string[]]$Platform = @(),

  [switch]$NoDeps
)

$ErrorActionPreference = 'Stop'

function Info([string]$msg) { Write-Host "[INFO] $msg" -ForegroundColor Green }
function Warn([string]$msg) { Write-Host "[WARN] $msg" -ForegroundColor Yellow }
function Fail([string]$msg) { Write-Host "[ERROR] $msg" -ForegroundColor Red; exit 1 }

if ([string]::IsNullOrWhiteSpace($Version)) { $Version = 'dev' }
$OutputDir = "dist"
$script:WebUIBuilt = $false

function Get-TagDisplay([string]$tags) {
  if ([string]::IsNullOrWhiteSpace($tags)) { return 'none' }
  return $tags
}

function Test-IsWindowsHost() {
  if (Get-Variable -Name IsWindows -Scope Global -ErrorAction SilentlyContinue) {
    return [bool]$global:IsWindows
  }
  return $env:OS -eq 'Windows_NT'
}

function Test-IsLinuxHost() {
  if (Get-Variable -Name IsLinux -Scope Global -ErrorAction SilentlyContinue) {
    return [bool]$global:IsLinux
  }
  return $false
}

function Append-Tags([string]$cur, [string]$add) {
  $add = $add.Trim().Trim(',')
  if ([string]::IsNullOrWhiteSpace($add)) { return $cur }
  if ([string]::IsNullOrWhiteSpace($cur)) { return $add }
  return "$cur,$add"
}

$BuildTags = ''
if (-not [string]::IsNullOrWhiteSpace($Tags)) { $BuildTags = Append-Tags $BuildTags $Tags }

function Variant-Suffix([string]$tags) {
  $t = ",$tags,"
  if ($t -like '*,proxy,*') { return '-proxy' }
  return ''
}

function Remove-Tag([string]$cur, [string]$remove) {
  if ([string]::IsNullOrWhiteSpace($cur)) { return '' }
  $parts = $cur.Split(',') | ForEach-Object { $_.Trim() } | Where-Object { $_ -and $_ -ne $remove }
  return [string]::Join(',', $parts)
}

function Detect-HostPlatform() {
  if (Test-IsWindowsHost) {
    $os = 'windows'
  } elseif (Test-IsLinuxHost) {
    $os = 'linux'
  } else {
    $os = 'darwin'
  }
  $arch = $env:PROCESSOR_ARCHITECTURE
  switch ($arch) {
    'AMD64' { $arch = 'amd64' }
    'ARM64' { $arch = 'arm64' }
    default { $arch = $arch.ToLowerInvariant() }
  }
  return "$os/$arch"
}

function Split-Platform([string]$p) {
  if ($p -notmatch '^[^/]+/[^/]+$') { Fail "Invalid -Platform '$p' (expected OS/ARCH)" }
  $parts = $p.Split('/')
  return @($parts[0], $parts[1])
}

function Ensure-Dir([string]$path) {
  if (-not (Test-Path $path)) { New-Item -ItemType Directory -Path $path | Out-Null }
}

function Check-Wails() {
  $wails = Get-Command wails -ErrorAction SilentlyContinue
  if (-not $wails) { Fail "Wails CLI not found. Install: go install github.com/wailsapp/wails/v2/cmd/wails@latest" }
}

function Install-Deps() {
  Info "Installing frontend dependencies..."
  Push-Location "desktop/ui"
  try {
    npm install
  } finally {
    Pop-Location
  }
}

function Install-WebUIDeps() {
  Info "Installing web UI dependencies..."
  Push-Location "web/ui"
  try {
    npm install
  } finally {
    Pop-Location
  }
}

function Build-WebUI() {
  if ($script:WebUIBuilt) { return }

  if (-not $NoDeps) { Install-WebUIDeps }

  Info "Building web UI..."
  Push-Location "web/ui"
  try {
    npm run build
  } finally {
    Pop-Location
  }

  $script:WebUIBuilt = $true
}

function Package-Desktop([string]$os, [string]$arch, [string]$variantName) {
  Ensure-Dir $OutputDir
  $base = "$variantName-$os-$arch"

  if ($os -eq 'windows') {
    $exe = "desktop/build/bin/cliSimpleHub.exe"
    if (-not (Test-Path $exe)) { $exe = "desktop/build/bin/CliSimpleHub.exe" }
    if (-not (Test-Path $exe)) { Warn "Desktop exe not found, skipping packaging"; return }
    Compress-Archive -Force -Path $exe -DestinationPath (Join-Path $OutputDir "$base.zip")
    Info "Created: $OutputDir/$base.zip"
    return
  }

  # On Windows, tar/gzip is not always available; zip is universal for packaging.
  if ($os -eq 'darwin') {
    $bin = "desktop/build/bin/CliSimpleHub.app"
  } else {
    $bin = "desktop/build/bin/cliSimpleHub"
    if (-not (Test-Path $bin)) { $bin = "desktop/build/bin/CliSimpleHub" }
  }
  if (-not (Test-Path $bin)) { Warn "Desktop binary not found, skipping packaging"; return }
  Compress-Archive -Force -Path $bin -DestinationPath (Join-Path $OutputDir "$base.zip")
  Info "Created: $OutputDir/$base.zip"
}

function Build-DesktopVariant([string]$os, [string]$arch, [string]$variantName, [string]$tags) {
  Check-Wails
  if (-not $NoDeps) { Install-Deps }

  if ((Test-IsWindowsHost) -and $os -ne 'windows') {
    Warn "Desktop cross-build from Windows to $os/$arch may require additional toolchain support; Wails build may fail."
  }

  $goflags = [string]$env:GOFLAGS
  if ($goflags -match '(^|\s)-tags=\S+') {
    Warn "GOFLAGS already contains -tags, overriding for this build"
    $goflags = ($goflags -replace '(^|\s)-tags=\S+', '').Trim()
  } else {
    $goflags = $goflags.Trim()
  }

  Info "Building desktop variant $variantName : $os/$arch tags=$(Get-TagDisplay $tags)"
  Push-Location "desktop"
  try {
    $originalGOFLAGS = $env:GOFLAGS
    $env:GOFLAGS = $goflags
    if ([string]::IsNullOrWhiteSpace($tags)) {
      wails build -platform "$os/$arch" -clean
    } else {
      wails build -platform "$os/$arch" -clean -tags $tags
    }
  } finally {
    $env:GOFLAGS = $originalGOFLAGS
    Pop-Location
  }

  Package-Desktop $os $arch $variantName
}

function Build-Desktop([string]$os, [string]$arch) {
  Build-WebUI

  $defaultTags = Remove-Tag $BuildTags 'proxy'
  $proxyTags = Append-Tags $defaultTags 'proxy'

  Build-DesktopVariant $os $arch 'cliSimpleHub' $defaultTags
  Build-DesktopVariant $os $arch 'cliSimpleHub-proxy' $proxyTags
}

function Package-Server([string]$os, [string]$arch, [string]$variantName, [string]$tags) {
  Ensure-Dir $OutputDir
  $base = "$variantName-$os-$arch"

  $staging = Join-Path $OutputDir ".staging"
  Ensure-Dir $staging

  $ext = if ($os -eq 'windows') { '.exe' } else { '' }
  $binName = "$(($variantName -replace '-proxy$',''))$ext"
  $binPath = Join-Path $staging $binName

  $tagsArgs = @()
  if (-not [string]::IsNullOrWhiteSpace($tags)) { $tagsArgs = @('-tags', $tags) }

  Info "Building server: $variantName $os/$arch tags=$(Get-TagDisplay $tags)"
  $env:GOOS = $os
  $env:GOARCH = $arch
  if (-not $env:CGO_ENABLED) { $env:CGO_ENABLED = '0' }
  go build -trimpath @tagsArgs -o $binPath ./cmd/server

  Compress-Archive -Force -Path $binPath -DestinationPath (Join-Path $OutputDir "$base.zip")
  Info "Created: $OutputDir/$base.zip"

  Remove-Item -Force $binPath -ErrorAction SilentlyContinue
  Remove-Item -Force $staging -Recurse -ErrorAction SilentlyContinue
}

function Resolve-Platforms() {
  if ($Platform.Count -gt 0) { return $Platform }

  $host = Detect-HostPlatform
  switch ($Command) {
    'current' { return @($host) }
    'macos' { return @('darwin/amd64','darwin/arm64') }
    'linux' { return @('linux/amd64','linux/arm64') }
    'windows' { return @('windows/amd64','windows/arm64') }
    'all' {
      if ($Target -eq 'desktop' -or $Target -eq 'both') {
        # Desktop "all" excludes windows cross-build by default.
        Warn "Desktop 'all' excludes Windows by default; use: .\\build.ps1 desktop windows"
        return @('darwin/amd64','darwin/arm64','linux/amd64','linux/arm64')
      }
      return @('darwin/amd64','darwin/arm64','linux/amd64','linux/arm64','windows/amd64','windows/arm64')
    }
    default { Fail "Unknown command: $Command" }
  }
}

function Clean() {
  Info "Cleaning build artifacts..."
  if (Test-Path $OutputDir) { Remove-Item -Force -Recurse $OutputDir }
  if (Test-Path "desktop/build/bin") { Remove-Item -Force -Recurse "desktop/build/bin" }
  if (Test-Path "internal/proxy/webui/dist") { Remove-Item -Force -Recurse "internal/proxy/webui/dist" }
  Info "Clean complete"
}

if ($Command -eq 'clean') {
  Clean
  exit 0
}

$platforms = Resolve-Platforms
foreach ($p in $platforms) {
  $os, $arch = Split-Platform $p
  if ($Target -eq 'desktop' -or $Target -eq 'both') {
    Build-Desktop $os $arch
  }
  if ($Target -eq 'server' -or $Target -eq 'both') {
    Build-WebUI
    $defaultTags = Remove-Tag $BuildTags 'proxy'
    $proxyTags = Append-Tags $defaultTags 'proxy'
    Package-Server $os $arch 'cliSimpleHub-server' $defaultTags
    Package-Server $os $arch 'cliSimpleHub-server-proxy' $proxyTags
  }
}

Info "Build complete! Output in: $OutputDir/"
