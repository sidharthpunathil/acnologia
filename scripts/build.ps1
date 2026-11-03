# Builds Acnologia without make or sh, for Windows. It does what the
# Makefile targets of the same names do:
#
#   .\scripts\build.ps1            # build: web + llms + bin\acnologia.exe
#   .\scripts\build.ps1 cli        # bin\acn.exe only (no web build needed)
#   .\scripts\build.ps1 llms       # copy the docs into the server packages
#   .\scripts\build.ps1 web        # build the dashboard into the server
#   .\scripts\build.ps1 test       # go test for the server and the Go SDK
#
# Needs Go (see server/go.mod) and, for web and build, Node with pnpm
# (npm install -g pnpm@10.24.0). Works in Windows PowerShell 5.1 and
# PowerShell 7, also on macOS and Linux.
[CmdletBinding()]
param(
	[ValidateSet('build', 'cli', 'llms', 'web', 'test')]
	[string]$Target = 'build',
	[string]$Version = ''
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$onWindows = $env:OS -eq 'Windows_NT'
$exe = ''
if ($onWindows) { $exe = '.exe' }

function Invoke-Native([string]$dir, [string]$file, [string[]]$argv) {
	Push-Location $dir
	try {
		& $file @argv
		if ($LASTEXITCODE -ne 0) { throw "$file $($argv -join ' ') failed with exit code $LASTEXITCODE" }
	} finally {
		Pop-Location
	}
}

function Get-Version {
	if ($Version) { return $Version }
	try {
		$v = (& git -C $root describe --tags --always --dirty 2>$null | Out-String).Trim()
		if ($LASTEXITCODE -eq 0 -and $v) { return $v }
	} catch { }
	return 'dev'
}

function Build-Web {
	Invoke-Native (Join-Path $root 'web') 'pnpm' @('install', '--frozen-lockfile')
	Invoke-Native (Join-Path $root 'web') 'pnpm' @('build')
}

function Copy-Docs {
	$docs = Join-Path $root 'docs'
	$llms = Join-Path $root 'server/internal/llmstxt'
	$files = Join-Path $root 'server/internal/docsmd/files'
	$assets = Join-Path $root 'server/internal/docsmd/assets'
	Copy-Item (Join-Path $docs 'llms.txt') $llms -Force
	Copy-Item (Join-Path $docs 'llms-full.txt') $llms -Force
	New-Item -ItemType Directory -Force -Path $files, $assets | Out-Null
	Copy-Item (Join-Path $root 'README.md') (Join-Path $files 'overview.md') -Force
	foreach ($name in @('architecture', 'flows', 'security', 'configuration', 'api', 'providers', 'cli', 'sdks', 'development', 'deploy', 'deploy-railway', 'deploy-vps')) {
		Copy-Item (Join-Path $docs "$name.md") $files -Force
	}
	Copy-Item (Join-Path $docs 'screenshots/*.png') $assets -Force
	Copy-Item (Join-Path $docs 'brand/logo.png') $assets -Force
}

function Build-Go([string]$cmd, [string]$out) {
	$v = Get-Version
	New-Item -ItemType Directory -Force -Path (Join-Path $root 'bin') | Out-Null
	$prev = $env:CGO_ENABLED
	$env:CGO_ENABLED = '0'
	try {
		Invoke-Native (Join-Path $root 'server') 'go' @('build', '-trimpath', '-ldflags', "-s -w -X main.version=$v", '-o', (Join-Path $root "bin/$out$exe"), "./cmd/$cmd")
	} finally {
		$env:CGO_ENABLED = $prev
	}
	Write-Host "Built bin\$out$exe ($v)"
}

switch ($Target) {
	'web' { Build-Web }
	'llms' { Copy-Docs }
	'cli' { Build-Go 'acn' 'acn' }
	'build' {
		Build-Web
		Copy-Docs
		Build-Go 'acnologia' 'acnologia'
	}
	'test' {
		Invoke-Native (Join-Path $root 'server') 'go' @('test', './...')
		Invoke-Native (Join-Path $root 'sdk/go') 'go' @('test', './...')
	}
}
