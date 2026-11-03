# Installs and starts the Acnologia server on Windows.
#
#   irm https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/setup.ps1 | iex
#
# It downloads the newest server release (a v* tag) for this CPU, checks it
# against the release's SHA256SUMS and puts everything in
# %LocalAppData%\Acnologia: bin\acnologia.exe, data\ (the database and
# master.key), .env and logs\. The .env listens on 127.0.0.1 only. A
# Scheduled Task starts it at sign in (no administrator prompt), then it waits
# for /healthz and prints the address. Running it again upgrades the server
# and keeps the data.
#
# Options, as environment variables set before the command:
#   $env:ACNOLOGIA_PORT = "9090"        listen on 127.0.0.1:9090 (default 8080)
#   $env:ACNOLOGIA_NO_SERVICE = "1"     no Scheduled Task, just start it now
#   $env:ACNOLOGIA_UNINSTALL = "1"      remove the task and binary, keep the data
#   $env:ACNOLOGIA_PURGE = "1"          with uninstall, also delete the data
#   $env:ACNOLOGIA_VERSION = "v0.1.0"   pick a release
#   $env:ACNOLOGIA_BINARY = "C:\path\acnologia.exe"   install your own build
#
# Works in Windows PowerShell 5.1 and PowerShell 7. On macOS and Linux run
# setup.sh instead:
#   curl -fsSL https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/setup.sh | sh

function Install-Acnologia {
	# Set here, not at the top: under `irm | iex` the script runs in the
	# caller's session, and these would stick there.
	$ErrorActionPreference = 'Stop'
	$ProgressPreference = 'SilentlyContinue'
	$repo = 'sidharthpunathil/acnologia'
	$taskName = 'Acnologia'

	function Say([string]$msg) { Write-Host $msg }
	function Step([string]$msg) { Write-Host ""; Write-Host "==> $msg" }
	function Fail([string]$msg) { throw "acnologia setup: $msg" }

	try {
		[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
	} catch { }

	if ($env:OS -ne 'Windows_NT') {
		Fail "this script is for Windows; on macOS and Linux run: curl -fsSL https://raw.githubusercontent.com/$repo/main/setup.sh | sh"
	}
	if (-not $env:LOCALAPPDATA) { Fail 'LOCALAPPDATA is not set' }

	$port = 8080
	if ($env:ACNOLOGIA_PORT) {
		if ($env:ACNOLOGIA_PORT -notmatch '^\d{1,5}$' -or [int]$env:ACNOLOGIA_PORT -lt 1 -or [int]$env:ACNOLOGIA_PORT -gt 65535) {
			Fail 'ACNOLOGIA_PORT must be a number between 1 and 65535'
		}
		$port = [int]$env:ACNOLOGIA_PORT
	}
	$home_ = Join-Path $env:LOCALAPPDATA 'Acnologia'
	$bin = Join-Path $home_ 'bin'
	$data = Join-Path $home_ 'data'
	$logs = Join-Path $home_ 'logs'
	$exe = Join-Path $bin 'acnologia.exe'
	$envFile = Join-Path $home_ '.env'
	$url = "http://127.0.0.1:$port"

	function Stop-Acnologia {
		$t = Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
		if ($t) { Stop-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue }
		Get-Process -Name 'acnologia' -ErrorAction SilentlyContinue |
			Where-Object { $_.Path -eq $exe } |
			Stop-Process -Force -ErrorAction SilentlyContinue
	}

	# ---- uninstall ----
	if ($env:ACNOLOGIA_UNINSTALL -eq '1') {
		Step 'Removing Acnologia'
		Stop-Acnologia
		Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction SilentlyContinue
		Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $bin, $logs
		if ($env:ACNOLOGIA_PURGE -eq '1') {
			Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $home_
			Say "Removed $home_ and everything in it, keys included."
		} else {
			Say "Removed the task and the binary. Your data is still in $data."
			Say 'Set $env:ACNOLOGIA_PURGE = "1" as well to delete it too.'
		}
		return
	}

	$cpu = $env:PROCESSOR_ARCHITEW6432
	if (-not $cpu) { $cpu = $env:PROCESSOR_ARCHITECTURE }
	switch ($cpu) {
		'AMD64' { $arch = 'amd64' }
		'ARM64' { $arch = 'arm64' }
		default { Fail "no Acnologia build for $cpu" }
	}

	$tmp = Join-Path ([IO.Path]::GetTempPath()) ("acnologia-" + [Guid]::NewGuid().ToString('N'))
	New-Item -ItemType Directory -Force -Path $tmp | Out-Null
	try {
		if ($env:ACNOLOGIA_BINARY) {
			if (-not (Test-Path -PathType Leaf $env:ACNOLOGIA_BINARY)) { Fail "ACNOLOGIA_BINARY is not a file: $env:ACNOLOGIA_BINARY" }
			Step "Installing $env:ACNOLOGIA_BINARY"
			Copy-Item $env:ACNOLOGIA_BINARY (Join-Path $tmp 'acnologia.exe')
		} else {
			$ua = 'acnologia-setup'
			$tag = $env:ACNOLOGIA_VERSION
			if (-not $tag) {
				try {
					$releases = Invoke-RestMethod -UseBasicParsing -UserAgent $ua -Uri "https://api.github.com/repos/$repo/releases?per_page=100"
				} catch {
					Fail "could not list the releases of $repo"
				}
				# foreach, not a pipeline: Windows PowerShell 5.1 passes a JSON
				# array down a pipeline as one object. Newest first.
				foreach ($r in $releases) {
					if ($r.tag_name -match '^v\d') { $tag = $r.tag_name; break }
				}
				if (-not $tag) { Fail "no server release found in $repo yet. Build from source instead: https://github.com/$repo#quick-start" }
			}
			$asset = "acnologia_windows_$arch.zip"
			$base = "https://github.com/$repo/releases/download/$tag"
			Step "Downloading Acnologia $tag for windows/$arch"
			$zip = Join-Path $tmp $asset
			$sums = Join-Path $tmp 'SHA256SUMS'
			try {
				Invoke-WebRequest -UseBasicParsing -UserAgent $ua -Uri "$base/$asset" -OutFile $zip
				Invoke-WebRequest -UseBasicParsing -UserAgent $ua -Uri "$base/SHA256SUMS" -OutFile $sums
			} catch {
				Fail "could not download $base/$asset"
			}
			$want = $null
			foreach ($line in Get-Content $sums) {
				$parts = $line -split '\s+'
				if ($parts.Count -ge 2 -and ($parts[1] -eq $asset -or $parts[1] -eq "*$asset")) { $want = $parts[0].ToLower() }
			}
			if (-not $want) { Fail "$asset is not listed in SHA256SUMS" }
			$got = (Get-FileHash -Algorithm SHA256 $zip).Hash.ToLower()
			if ($want -ne $got) { Fail "checksum mismatch for $asset (want $want, got $got); not installing" }
			Expand-Archive -Path $zip -DestinationPath $tmp -Force
			if (-not (Test-Path (Join-Path $tmp 'acnologia.exe'))) { Fail 'the archive has no acnologia.exe' }
		}

		Stop-Acnologia
		New-Item -ItemType Directory -Force -Path $bin, $data, $logs | Out-Null
		Copy-Item -Force (Join-Path $tmp 'acnologia.exe') $exe
	} finally {
		Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $tmp
	}

	# .env: written once, kept on upgrade except for the listen address.
	if (-not (Test-Path $envFile)) {
		@(
			"# Acnologia settings. Every option: https://github.com/$repo/blob/main/docs/configuration.md",
			"ACNOLOGIA_DATA_DIR=$data",
			"ACNOLOGIA_ADDR=127.0.0.1:$port",
			'# Turn on the AI assistant, builder and offer refresh:',
			'# ANTHROPIC_API_KEY='
		) | Set-Content -Encoding ASCII $envFile
	} else {
		(Get-Content $envFile) -replace '^ACNOLOGIA_ADDR=.*', "ACNOLOGIA_ADDR=127.0.0.1:$port" | Set-Content -Encoding ASCII $envFile
	}

	# The server reads .env from its working folder; cmd redirects the log.
	$log = Join-Path $logs 'acnologia.log'
	$cmdArgs = "/c `"`"$exe`" >> `"$log`" 2>&1`""
	if ($env:ACNOLOGIA_NO_SERVICE -eq '1') {
		Step 'Starting Acnologia'
		Start-Process -FilePath 'cmd.exe' -ArgumentList $cmdArgs -WorkingDirectory $home_ -WindowStyle Hidden
		$stop = "Stop-Process -Name acnologia"
	} else {
		Step "Starting it as a Scheduled Task ($taskName) that runs at sign in"
		$action = New-ScheduledTaskAction -Execute 'cmd.exe' -Argument $cmdArgs -WorkingDirectory $home_
		$trigger = New-ScheduledTaskTrigger -AtLogOn -User "$env:USERDOMAIN\$env:USERNAME"
		$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
		Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Settings $settings -Description 'Acnologia key server' -Force | Out-Null
		Start-ScheduledTask -TaskName $taskName
		$stop = "Stop-ScheduledTask -TaskName $taskName"
	}

	Step "Waiting for $url"
	$ok = $false
	for ($i = 0; $i -lt 60; $i++) {
		try {
			$r = Invoke-WebRequest -UseBasicParsing -TimeoutSec 2 -Uri "$url/healthz"
			if ($r.StatusCode -eq 200) { $ok = $true; break }
		} catch { }
		Start-Sleep -Milliseconds 500
	}
	if (-not $ok) {
		Say 'It did not answer within 30 seconds. The log says:'
		if (Test-Path $log) { Get-Content -Tail 20 $log | ForEach-Object { Say $_ } }
		Fail 'Acnologia did not start'
	}

	Say ''
	Say "Acnologia is running at $url"
	Say ''
	Say '  Open it in a browser: the first visit creates the superadmin account.'
	Say "  Data and the master key: $data (back up master.key: without it the stored keys cannot be read)"
	Say "  Logs:     Get-Content -Wait $log"
	Say "  Stop:     $stop"
	Say '  Upgrade:  run this script again'
	Say "  Remove:   `$env:ACNOLOGIA_UNINSTALL = '1'; irm https://raw.githubusercontent.com/$repo/main/setup.ps1 | iex"
}

Install-Acnologia
