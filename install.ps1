# Installs acn, the Acnologia command line client, on Windows.
#
#   irm https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/install.ps1 | iex
#
# It downloads the newest cli-v* release for this CPU (amd64 or arm64),
# checks it against the release's SHA256SUMS, puts acn.exe in
# %LocalAppData%\Programs\acn and adds that folder to your user PATH. It
# never needs an administrator prompt.
#
# $env:ACN_VERSION = "cli-v0.1.0" picks a release, $env:ACN_INSTALL_DIR a
# place. Works in Windows PowerShell 5.1 and PowerShell 7.

function Install-Acn {
	# Set here, not at the top: under `irm | iex` the script runs in the
	# caller's session, and these would stick there.
	$ErrorActionPreference = 'Stop'
	$ProgressPreference = 'SilentlyContinue'
	$repo = 'sidharthpunathil/acnologia'

	function Say([string]$msg) { Write-Host $msg }
	function Fail([string]$msg) { throw "acn install: $msg" }

	# Windows PowerShell 5.1 may still default to TLS 1.0, which GitHub refuses.
	try {
		[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
	} catch { }

	if ($env:OS -ne 'Windows_NT') {
		Fail "this script is for Windows; on macOS and Linux run: curl -fsSL https://raw.githubusercontent.com/$repo/main/install.sh | sh"
	}

	# PROCESSOR_ARCHITEW6432 is set when 32 bit PowerShell runs on a 64 bit
	# Windows; it names the real CPU.
	$cpu = $env:PROCESSOR_ARCHITEW6432
	if (-not $cpu) { $cpu = $env:PROCESSOR_ARCHITECTURE }
	switch ($cpu) {
		'AMD64' { $arch = 'amd64' }
		'ARM64' { $arch = 'arm64' }
		default { Fail "no acn build for $cpu" }
	}

	# -UserAgent, not a header: Windows PowerShell 5.1 refuses a User-Agent
	# passed in -Headers.
	$ua = 'acn-install'
	$tag = $env:ACN_VERSION
	if (-not $tag) {
		try {
			$releases = Invoke-RestMethod -UseBasicParsing -UserAgent $ua -Uri "https://api.github.com/repos/$repo/releases?per_page=100"
		} catch {
			Fail "could not list the releases of $repo"
		}
		# foreach, not a pipeline: Windows PowerShell 5.1 passes a JSON array
		# down a pipeline as one object. The API lists the newest first.
		foreach ($r in $releases) {
			if ($r.tag_name -like 'cli-v*') { $tag = $r.tag_name; break }
		}
		if (-not $tag) { Fail "no cli-v release found in $repo" }
	}

	$asset = "acn_windows_$arch.zip"
	$base = "https://github.com/$repo/releases/download/$tag"
	$tmp = Join-Path ([IO.Path]::GetTempPath()) ("acn-" + [Guid]::NewGuid().ToString('N'))
	New-Item -ItemType Directory -Path $tmp | Out-Null
	try {
		Say "Downloading acn $tag for windows/$arch"
		$zip = Join-Path $tmp $asset
		$sums = Join-Path $tmp 'SHA256SUMS'
		try {
			Invoke-WebRequest -UseBasicParsing -UserAgent $ua -Uri "$base/$asset" -OutFile $zip
		} catch {
			Fail "could not download $base/$asset"
		}
		try {
			Invoke-WebRequest -UseBasicParsing -UserAgent $ua -Uri "$base/SHA256SUMS" -OutFile $sums
		} catch {
			Fail "could not download $base/SHA256SUMS"
		}

		$want = $null
		foreach ($line in Get-Content -LiteralPath $sums) {
			$parts = $line.Trim() -split '\s+', 2
			if ($parts.Count -eq 2 -and ($parts[1] -eq $asset -or $parts[1] -eq "*$asset")) {
				$want = $parts[0].ToLowerInvariant()
				break
			}
		}
		if (-not $want) { Fail "$asset is not listed in SHA256SUMS" }
		$got = (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash.ToLowerInvariant()
		if ($want -ne $got) { Fail "checksum mismatch for $asset (want $want, got $got); not installing" }

		$x = Join-Path $tmp 'x'
		Expand-Archive -LiteralPath $zip -DestinationPath $x -Force
		$exe = Join-Path $x 'acn.exe'
		if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) { Fail 'the archive has no acn.exe' }

		$dest = $env:ACN_INSTALL_DIR
		if (-not $dest) {
			if (-not $env:LOCALAPPDATA) { Fail 'LOCALAPPDATA is not set; pass ACN_INSTALL_DIR' }
			$dest = Join-Path $env:LOCALAPPDATA 'Programs\acn'
		}
		try {
			New-Item -ItemType Directory -Path $dest -Force | Out-Null
		} catch {
			Fail "cannot create $dest"
		}

		# Copy beside the old binary, then swap. A running acn.exe cannot be
		# overwritten, but it can be renamed out of the way.
		$target = Join-Path $dest 'acn.exe'
		$new = Join-Path $dest '.acn.new.exe'
		$old = Join-Path $dest '.acn.old.exe'
		try {
			Copy-Item -LiteralPath $exe -Destination $new -Force
		} catch {
			Fail "cannot write to $dest; pick another with ACN_INSTALL_DIR"
		}
		Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue
		if (Test-Path -LiteralPath $target) {
			try {
				Move-Item -LiteralPath $target -Destination $old -Force
			} catch {
				Remove-Item -LiteralPath $new -Force -ErrorAction SilentlyContinue
				Fail "cannot replace $target; close any running acn and try again"
			}
		}
		Move-Item -LiteralPath $new -Destination $target -Force
		Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue

		$version = 'acn'
		try { $version = (& $target version 2>$null | Out-String).Trim() } catch { }
		if (-not $version) { $version = 'acn' }
		Say "Installed $version to $target"

		# Add the folder to the user PATH (HKCU, no admin) and to this session.
		$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
		$entries = @()
		if ($userPath) { $entries = $userPath -split ';' | Where-Object { $_ } }
		$onPath = $entries | Where-Object { $_.TrimEnd('\') -ieq $dest.TrimEnd('\') }
		if (-not $onPath) {
			[Environment]::SetEnvironmentVariable('Path', (($entries + $dest) -join ';'), 'User')
			Say ''
			Say "Added $dest to your user PATH. Open a new terminal to pick it up."
		}
		if (-not (($env:Path -split ';') | Where-Object { $_.TrimEnd('\') -ieq $dest.TrimEnd('\') })) {
			$env:Path = "$env:Path;$dest"
		}
		Say ''
		Say 'Next: acn login'
	} finally {
		Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
	}
}

try {
	Install-Acn
} catch {
	[Console]::Error.WriteLine($_.Exception.Message)
	# Run as a file, fail the process; under `irm | iex` an exit would close
	# the user's terminal, so only set the exit code.
	if ($MyInvocation.MyCommand.Path) { exit 1 }
	$global:LASTEXITCODE = 1
}
