#!/bin/sh
# Installs and starts the Acnologia server on this machine.
#
#   curl -fsSL https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/setup.sh | sh
#
# What it does:
#   1. Downloads the newest server release (a v* tag) for this OS and CPU and
#      checks it against the release's SHA256SUMS. With --docker, or when there
#      is no release yet and Docker is here, it runs the container image instead.
#   2. Puts everything under ~/.acnologia: bin/acnologia, data/ (the database
#      and master.key), .env and logs/.
#   3. Writes a .env that listens on 127.0.0.1 only. Public servers belong
#      behind HTTPS: see deploy/install-server.sh and docs/deploy.md.
#   4. Runs it as a background service that starts at login and restarts on a
#      crash: a launchd agent on macOS, a systemd user unit on Linux.
#   5. Waits for /healthz and prints the address.
#
# Running it again upgrades the server and keeps the data. It never calls sudo.
#
# Options (after "sh -s --" when piped, for example: | sh -s -- --port 9090):
#   --port N          listen on 127.0.0.1:N (default 8080)
#   --no-service      no login service: start it in the background for now
#   --docker          run the container image instead of the binary
#   --with-cli        also install acn, the command line client
#   --uninstall       stop and remove the service and binary, keep the data
#   --purge           with --uninstall, also delete the data (keys included)
#
# Environment: ACNOLOGIA_VERSION=v0.1.0 picks a release, ACNOLOGIA_HOME sets
# the folder (default ~/.acnologia), ACNOLOGIA_BINARY=/path/acnologia installs
# a binary you built yourself instead of downloading one.
#
# Windows: run setup.ps1 from PowerShell instead:
#   irm https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/setup.ps1 | iex
set -eu

REPO="sidharthpunathil/acnologia"
IMAGE="ghcr.io/sidharthpunathil/acnologia:latest"
LABEL="dev.acnologia.server"

say() { printf '%s\n' "$*"; }
step() { printf '\n==> %s\n' "$*"; }
fail() {
	printf 'acnologia setup: %s\n' "$*" >&2
	exit 1
}

port=8080
service=1
mode=auto
with_cli=0
uninstall=0
purge=0
while [ $# -gt 0 ]; do
	case "$1" in
	--port)
		[ $# -ge 2 ] || fail "--port needs a number"
		port="$2"
		shift
		;;
	--port=*) port="${1#*=}" ;;
	--no-service) service=0 ;;
	--docker) mode=docker ;;
	--with-cli) with_cli=1 ;;
	--uninstall) uninstall=1 ;;
	--purge) purge=1 ;;
	-h | --help)
		sed -n '2,34p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//' || true
		exit 0
		;;
	*) fail "unknown option $1 (try --help)" ;;
	esac
	shift
done
case "$port" in
'' | *[!0-9]*) fail "--port must be a number" ;;
esac
[ "$port" -ge 1 ] && [ "$port" -le 65535 ] || fail "--port must be between 1 and 65535"

[ -n "${HOME:-}" ] || fail "HOME is not set"
home="${ACNOLOGIA_HOME:-$HOME/.acnologia}"
case "$home" in
/ | "$HOME" | "$HOME/") fail "ACNOLOGIA_HOME must be a folder of its own, not $home" ;;
esac
url="http://127.0.0.1:$port"

case "$(uname -s)" in
Darwin) os=darwin ;;
Linux) os=linux ;;
MINGW* | MSYS* | CYGWIN* | Windows_NT) fail "on Windows run this in PowerShell instead: irm https://raw.githubusercontent.com/$REPO/main/setup.ps1 | iex" ;;
*) fail "no Acnologia build for $(uname -s); see https://github.com/$REPO#quick-start" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) fail "no Acnologia build for $(uname -m)" ;;
esac

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL "$1" -o "$2"; }
	probe() { curl -fsS -o /dev/null --max-time 2 "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q -O "$2" "$1"; }
	probe() { wget -q -O /dev/null --timeout=2 "$1"; }
else
	fail "needs curl or wget"
fi

have_systemd_user() {
	command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1
}
plist="$HOME/Library/LaunchAgents/$LABEL.plist"
unit="$HOME/.config/systemd/user/acnologia.service"

stop_service() {
	if [ "$os" = darwin ] && [ -f "$plist" ]; then
		launchctl bootout "gui/$(id -u)" "$plist" 2>/dev/null || launchctl unload "$plist" 2>/dev/null || true
	elif [ "$os" = linux ] && [ -f "$unit" ] && have_systemd_user; then
		systemctl --user stop acnologia.service 2>/dev/null || true
	fi
	if command -v docker >/dev/null 2>&1 && docker inspect acnologia >/dev/null 2>&1; then
		docker rm -f acnologia >/dev/null 2>&1 || true
	fi
	if [ -f "$home/acnologia.pid" ]; then
		kill "$(cat "$home/acnologia.pid")" 2>/dev/null || true
		rm -f "$home/acnologia.pid"
	fi
}

# ---- uninstall ----
if [ "$uninstall" = 1 ]; then
	step "Removing Acnologia"
	stop_service
	if [ "$os" = darwin ]; then
		rm -f "$plist"
	elif [ -f "$unit" ]; then
		if have_systemd_user; then systemctl --user disable acnologia.service 2>/dev/null || true; fi
		rm -f "$unit"
		if have_systemd_user; then systemctl --user daemon-reload 2>/dev/null || true; fi
	fi
	for d in /usr/local/bin "$HOME/.local/bin"; do
		if [ -L "$d/acnologia" ] && [ "$(readlink "$d/acnologia")" = "$home/bin/acnologia" ]; then rm -f "$d/acnologia"; fi
	done
	rm -rf "${home:?}/bin" "${home:?}/logs"
	if [ "$purge" = 1 ]; then
		rm -rf "${home:?}"
		if command -v docker >/dev/null 2>&1; then docker volume rm acnologia-data >/dev/null 2>&1 || true; fi
		say "Removed $home and everything in it, keys included."
	else
		say "Removed the service and the binary. Your data is still in $home/data"
		say "(and the Docker volume acnologia-data, if you used --docker)."
		say "Run with --uninstall --purge to delete it too."
	fi
	exit 0
fi

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t acnologia)
trap 'rm -rf "$tmp"' EXIT INT TERM

# ---- pick binary or Docker ----
tag="${ACNOLOGIA_VERSION:-}"
if [ "$mode" = auto ] && [ -z "${ACNOLOGIA_BINARY:-}" ] && [ -z "$tag" ]; then
	if fetch "https://api.github.com/repos/$REPO/releases?per_page=100" "$tmp/releases.json" 2>/dev/null; then
		tag=$(grep -o '"tag_name": *"v[0-9][^"]*"' "$tmp/releases.json" | head -n 1 | sed 's/.*"\(v[^"]*\)"$/\1/')
	fi
	if [ -z "$tag" ]; then
		if command -v docker >/dev/null 2>&1; then
			say "No server release yet; using the container image instead."
			mode=docker
		else
			fail "no server release found in $REPO and Docker is not installed. Build from source instead: https://github.com/$REPO#quick-start"
		fi
	fi
fi
[ "$mode" = auto ] && mode=binary

mkdir -p "$home/data" "$home/logs"
chmod 700 "$home" "$home/data"

# ---- Docker ----
if [ "$mode" = docker ]; then
	command -v docker >/dev/null 2>&1 || fail "--docker needs Docker"
	docker info >/dev/null 2>&1 || fail "Docker is installed but not running; start it and run this again"
	step "Pulling $IMAGE"
	docker pull "$IMAGE" >/dev/null || fail "could not pull $IMAGE"
	stop_service
	step "Starting the container"
	docker run -d --name acnologia --restart unless-stopped \
		-p "127.0.0.1:$port:8080" -v acnologia-data:/data "$IMAGE" >/dev/null ||
		fail "could not start the container"
	where="the Docker volume acnologia-data"
	logs="docker logs -f acnologia"
	stop_cmd="docker stop acnologia"
else
	# ---- binary ----
	if [ -n "${ACNOLOGIA_BINARY:-}" ]; then
		[ -x "$ACNOLOGIA_BINARY" ] || fail "ACNOLOGIA_BINARY is not an executable file: $ACNOLOGIA_BINARY"
		cp "$ACNOLOGIA_BINARY" "$tmp/acnologia"
		step "Installing $ACNOLOGIA_BINARY"
	else
		if command -v sha256sum >/dev/null 2>&1; then
			sha256() { sha256sum "$1" | awk '{print $1}'; }
		elif command -v shasum >/dev/null 2>&1; then
			sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
		else
			fail "needs sha256sum or shasum to check the download"
		fi
		asset="acnologia_${os}_${arch}.tar.gz"
		base="https://github.com/$REPO/releases/download/$tag"
		step "Downloading Acnologia $tag for $os/$arch"
		fetch "$base/$asset" "$tmp/$asset" || fail "could not download $base/$asset"
		fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || fail "could not download $base/SHA256SUMS"
		want=$(awk -v a="$asset" '$2 == a || $2 == "*" a {print $1}' "$tmp/SHA256SUMS")
		[ -n "$want" ] || fail "$asset is not listed in SHA256SUMS"
		got=$(sha256 "$tmp/$asset")
		[ "$want" = "$got" ] || fail "checksum mismatch for $asset (want $want, got $got); not installing"
		tar -xzf "$tmp/$asset" -C "$tmp"
		[ -f "$tmp/acnologia" ] || fail "the archive has no acnologia binary"
	fi
	stop_service
	mkdir -p "$home/bin"
	cp "$tmp/acnologia" "$home/bin/.acnologia.new"
	chmod 0755 "$home/bin/.acnologia.new"
	mv -f "$home/bin/.acnologia.new" "$home/bin/acnologia"
	# macOS refuses to run a downloaded binary that is still quarantined.
	if [ "$os" = darwin ]; then xattr -d com.apple.quarantine "$home/bin/acnologia" 2>/dev/null || true; fi

	# A command on PATH, pointing at the installed binary.
	link=""
	for d in /usr/local/bin "$HOME/.local/bin"; do
		if [ -d "$d" ] && [ -w "$d" ]; then
			ln -sf "$home/bin/acnologia" "$d/acnologia" && link="$d/acnologia" && break
		fi
	done

	# .env: written once, kept on upgrade except for the listen address.
	if [ ! -f "$home/.env" ]; then
		cat >"$home/.env" <<EOF
# Acnologia settings. Every option: https://github.com/$REPO/blob/main/docs/configuration.md
ACNOLOGIA_DATA_DIR=$home/data
ACNOLOGIA_ADDR=127.0.0.1:$port
# Turn on the AI assistant, builder and offer refresh:
# ANTHROPIC_API_KEY=
EOF
		chmod 600 "$home/.env"
	else
		sed "s|^ACNOLOGIA_ADDR=.*|ACNOLOGIA_ADDR=127.0.0.1:$port|" "$home/.env" >"$home/.env.new" && mv -f "$home/.env.new" "$home/.env"
		chmod 600 "$home/.env"
	fi

	where="$home/data"
	logs="tail -f $home/logs/acnologia.log"

	if [ "$service" = 1 ] && [ "$os" = darwin ]; then
		step "Starting it as a launchd agent ($LABEL)"
		mkdir -p "$HOME/Library/LaunchAgents"
		cat >"$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key><array><string>$home/bin/acnologia</string></array>
  <key>WorkingDirectory</key><string>$home</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$home/logs/acnologia.log</string>
  <key>StandardErrorPath</key><string>$home/logs/acnologia.log</string>
</dict>
</plist>
EOF
		launchctl bootstrap "gui/$(id -u)" "$plist" 2>/dev/null || launchctl load -w "$plist" ||
			fail "launchctl could not load $plist"
		stop_cmd="launchctl bootout gui/$(id -u) $plist"
	elif [ "$service" = 1 ] && [ "$os" = linux ] && have_systemd_user; then
		step "Starting it as a systemd user service (acnologia.service)"
		mkdir -p "$(dirname "$unit")"
		cat >"$unit" <<EOF
[Unit]
Description=Acnologia key server
After=network-online.target

[Service]
WorkingDirectory=$home
ExecStart=$home/bin/acnologia
Restart=on-failure
RestartSec=3
StandardOutput=append:$home/logs/acnologia.log
StandardError=append:$home/logs/acnologia.log

[Install]
WantedBy=default.target
EOF
		systemctl --user daemon-reload
		systemctl --user enable --now acnologia.service >/dev/null 2>&1 ||
			fail "systemctl could not start acnologia.service; see: journalctl --user -u acnologia"
		stop_cmd="systemctl --user stop acnologia"
	else
		if [ "$service" = 1 ]; then
			say "No launchd or systemd user session here, so it runs in the background until you log out or reboot."
		fi
		step "Starting Acnologia"
		# exec, with every stream detached, so nothing keeps this script's
		# output open after it exits (curl ... | sh would never return).
		(cd "$home" && exec nohup "$home/bin/acnologia" </dev/null >>"$home/logs/acnologia.log" 2>&1) &
		echo $! >"$home/acnologia.pid"
		stop_cmd="kill \$(cat $home/acnologia.pid)"
	fi
fi

# ---- wait until it answers ----
step "Waiting for $url"
i=0
until probe "$url/healthz" 2>/dev/null; do
	i=$((i + 1))
	if [ "$i" -ge 60 ]; then
		say "It did not answer within 30 seconds. The log says:"
		if [ "$mode" = docker ]; then docker logs --tail 20 acnologia 2>&1 || true; else tail -n 20 "$home/logs/acnologia.log" 2>/dev/null || true; fi
		fail "Acnologia did not start"
	fi
	sleep 0.5
done

if [ "$with_cli" = 1 ]; then
	step "Installing acn, the command line client"
	if fetch "https://raw.githubusercontent.com/$REPO/main/install.sh" "$tmp/install.sh"; then
		sh "$tmp/install.sh" || say "Could not install acn; the server is fine. Try later: curl -fsSL https://raw.githubusercontent.com/$REPO/main/install.sh | sh"
	else
		say "Could not download the acn installer; the server is fine."
	fi
fi

cat <<EOF

Acnologia is running at $url

  Open it in a browser: the first visit creates the superadmin account.
  Data and the master key: $where (back up master.key: without it the stored keys cannot be read)
  Logs:     $logs
  Stop:     $stop_cmd
  Upgrade:  run this script again
  Remove:   curl -fsSL https://raw.githubusercontent.com/$REPO/main/setup.sh | sh -s -- --uninstall
EOF
if [ "$mode" = binary ] && [ -n "${link:-}" ]; then
	say "  Command:  $link"
fi
if [ "$mode" = binary ] && [ "$os" = linux ] && [ "$service" = 1 ] && have_systemd_user; then
	say ""
	say "To keep it running after you log out: loginctl enable-linger \"\$USER\""
fi
