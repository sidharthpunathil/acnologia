#!/usr/bin/env bash
# Installs Acnologia on any Linux server (Ubuntu, Debian, Fedora, RHEL,
# Amazon Linux and the rest Docker supports), with HTTPS from Let's Encrypt.
#
#   curl -fsSL https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/deploy/install-server.sh \
#     | sudo bash -s -- --domain key.example.com --email you@example.com
#
# With no --domain it serves https://<public-ip>.sslip.io, so HTTPS works
# before you own a domain. Run it again to upgrade: settings and data stay.
#
# What it does:
#   1. installs Docker (get.docker.com) if it is missing
#   2. writes /opt/acnologia/.env once, with a random master key and access
#      password, and never overwrites it
#   3. writes a compose file that runs the acnologia image and Caddy, which
#      gets the certificate and proxies to the server
#   4. pulls the image (or builds it from source with --build), starts it and
#      waits for /healthz
#
# Options (each also works as an environment variable, shown in brackets):
#   --domain NAME        public host name pointing at this server   [ACN_DOMAIN]
#   --email ADDR         Let's Encrypt contact address              [ACN_EMAIL]
#   --no-tls             no Caddy: plain HTTP on --port (for a server
#                        behind your own proxy or load balancer)    [ACN_NO_TLS=1]
#   --public-url URL     the address users reach, with --no-tls     [ACN_PUBLIC_URL]
#   --port N             host port, default 8080, loopback only
#                        unless --no-tls                            [ACN_PORT]
#   --dir PATH           install directory, default /opt/acnologia  [ACN_DIR]
#   --image REF          image to run                               [ACN_IMAGE]
#   --build              build the image from source instead of pulling
#                                                                   [ACN_BUILD=1]
#   --ref REF            git branch or tag to build, default main   [ACN_REF]
#   --anthropic-key KEY  turns on the AI features            [ANTHROPIC_API_KEY]
#   --uninstall          stop and remove the containers (keeps data and .env)
#   -h, --help           show this help
set -euo pipefail

REPO_URL="https://github.com/sidharthpunathil/acnologia"
DEFAULT_IMAGE="ghcr.io/sidharthpunathil/acnologia:latest"

# Options from the environment; the command line wins over them, and a
# rerun falls back to the choices saved in $DIR/install.conf.
DOMAIN="${ACN_DOMAIN:-}"
EMAIL="${ACN_EMAIL:-}"
NO_TLS="${ACN_NO_TLS:-}"
PORT="${ACN_PORT:-}"
PUBLIC_URL="${ACN_PUBLIC_URL:-}"
DIR="${ACN_DIR:-/opt/acnologia}"
IMAGE="${ACN_IMAGE:-}"
BUILD="${ACN_BUILD:-}"
REF="${ACN_REF:-}"
AI_KEY="${ANTHROPIC_API_KEY:-}"
UNINSTALL=0

say() { printf '\033[1m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[33mwarning:\033[0m %s\n' "$*" >&2; }
die() { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
	cat <<'USAGE'
Usage: install-server.sh [options]   (run as root)

  --domain NAME        public host name pointing at this server
                       (default: <public-ip>.sslip.io)
  --email ADDR         Let's Encrypt contact address
  --no-tls             plain HTTP on --port, no Caddy (behind your own proxy)
  --public-url URL     the address users reach, with --no-tls
  --port N             host port (default 8080; loopback only unless --no-tls)
  --dir PATH           install directory (default /opt/acnologia)
  --image REF          image to run (default ghcr.io/sidharthpunathil/acnologia:latest)
  --build              build the image from source instead of pulling it
  --ref REF            git branch or tag to build (default main)
  --anthropic-key KEY  turns on the AI features
  --uninstall          stop and remove the containers, keep data and .env

Run it again to upgrade. Docs: https://github.com/sidharthpunathil/acnologia/blob/main/docs/deploy-vps.md
USAGE
}

while [ $# -gt 0 ]; do
	case "$1" in
	--domain) DOMAIN="${2:?--domain needs a value}"; shift 2 ;;
	--email) EMAIL="${2:?--email needs a value}"; shift 2 ;;
	--no-tls) NO_TLS=1; shift ;;
	--public-url) PUBLIC_URL="${2:?--public-url needs a value}"; shift 2 ;;
	--port) PORT="${2:?--port needs a value}"; shift 2 ;;
	--dir) DIR="${2:?--dir needs a value}"; shift 2 ;;
	--image) IMAGE="${2:?--image needs a value}"; shift 2 ;;
	--build) BUILD=1; shift ;;
	--ref) REF="${2:?--ref needs a value}"; shift 2 ;;
	--anthropic-key) AI_KEY="${2:?--anthropic-key needs a value}"; shift 2 ;;
	--uninstall) UNINSTALL=1; shift ;;
	-h | --help) usage; exit 0 ;;
	*) die "unknown option: $1 (see --help)" ;;
	esac
done

# A rerun keeps what the last run chose unless told otherwise.
CONF="$DIR/install.conf"
if [ -f "$CONF" ]; then
	while IFS='=' read -r k v; do
		case "$k" in
		DOMAIN) DOMAIN="${DOMAIN:-$v}" ;;
		EMAIL) EMAIL="${EMAIL:-$v}" ;;
		NO_TLS) NO_TLS="${NO_TLS:-$v}" ;;
		PORT) PORT="${PORT:-$v}" ;;
		PUBLIC_URL) PUBLIC_URL="${PUBLIC_URL:-$v}" ;;
		IMAGE) IMAGE="${IMAGE:-$v}" ;;
		BUILD) BUILD="${BUILD:-$v}" ;;
		REF) REF="${REF:-$v}" ;;
		esac
	done <"$CONF"
fi
NO_TLS="${NO_TLS:-0}"
PORT="${PORT:-8080}"
IMAGE="${IMAGE:-$DEFAULT_IMAGE}"
BUILD="${BUILD:-0}"
REF="${REF:-main}"

# Everything the installer writes is for root only.
umask 077

[ "$(uname -s)" = Linux ] || die "this installer is for Linux servers; on macOS or Windows use docker compose (see the README)"
[ "$(id -u)" -eq 0 ] || die "run it as root, for example: curl ... | sudo bash -s -- --domain key.example.com"
case "$PORT" in '' | *[!0-9]*) die "--port must be a number" ;; esac

compose() { docker compose --project-directory "$DIR" -f "$DIR/compose.yml" "$@"; }

if [ "$UNINSTALL" = 1 ]; then
	[ -f "$DIR/compose.yml" ] || die "nothing installed in $DIR"
	compose down
	say "Stopped. Data and settings are still in $DIR (delete the folder to remove them)."
	exit 0
fi

# Random secrets from the kernel, no extra packages needed.
rand_b64() { head -c "$1" /dev/urandom | base64 | tr -d '\n'; }
rand_pw() { head -c 48 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | cut -c1-28; }

public_ip() {
	local ip=""
	for url in https://api.ipify.org https://ifconfig.me/ip https://icanhazip.com; do
		ip="$(curl -fsS4 --max-time 5 "$url" 2>/dev/null | tr -d '[:space:]')" || true
		case "$ip" in *.*.*.*) printf '%s' "$ip"; return 0 ;; esac
	done
	return 1
}

# 1. Docker.
if ! command -v docker >/dev/null 2>&1; then
	say "Installing Docker"
	command -v curl >/dev/null 2>&1 || die "curl is needed to install Docker"
	curl -fsSL https://get.docker.com | sh
fi
if command -v systemctl >/dev/null 2>&1; then
	systemctl enable --now docker >/dev/null 2>&1 || true
fi
docker compose version >/dev/null 2>&1 || die "the docker compose plugin is missing; install docker-compose-plugin and run this again"

# 2. Where it is served. Only an address you gave is saved; one worked out
# from the server's IP is worked out again on the next run.
saved_url="$PUBLIC_URL"
if [ "$NO_TLS" = 1 ]; then
	if [ -z "$PUBLIC_URL" ]; then
		PUBLIC_URL="http://$(public_ip || echo localhost):$PORT"
	fi
else
	if [ -z "$DOMAIN" ]; then
		ip="$(public_ip)" || die "could not find this server's public IPv4 address; pass --domain"
		DOMAIN="$(echo "$ip" | tr . -).sslip.io"
		say "No --domain given, using $DOMAIN (point a real domain here later and run again with --domain)"
	fi
	PUBLIC_URL="https://$DOMAIN"
fi

mkdir -p "$DIR/data" "$DIR/caddy"
chmod 700 "$DIR"
# A domain picked automatically (sslip.io) is not saved, so a rerun after the
# IP changes picks the new one.
case "$DOMAIN" in *.sslip.io) saved_domain="" ;; *) saved_domain="$DOMAIN" ;; esac
cat >"$CONF" <<EOF
DOMAIN=$saved_domain
EMAIL=$EMAIL
NO_TLS=$NO_TLS
PORT=$PORT
PUBLIC_URL=$([ "$NO_TLS" = 1 ] && printf '%s' "$saved_url")
IMAGE=$([ "$BUILD" = 1 ] || printf '%s' "$IMAGE")
BUILD=$BUILD
REF=$REF
EOF
chmod 600 "$CONF"
# The image runs as the distroless nonroot user; -R also fixes data copied
# in from another server.
chown -R 65532:65532 "$DIR/data"

# 3. Settings, written once. Later runs only update ACNOLOGIA_PUBLIC_URL,
# the two proxy settings when Caddy is in front (with --no-tls they are
# yours, except that plain HTTP forces SECURE_COOKIES off) and
# ANTHROPIC_API_KEY when --anthropic-key is given. Everything else you edit
# stays.
ENV_FILE="$DIR/.env"
set_env() {
	local key="$1" value="$2"
	if grep -q "^$key=" "$ENV_FILE"; then
		local tmp
		tmp="$(mktemp)"
		awk -v k="$key" -v v="$value" 'BEGIN{FS=OFS="="} $1==k {print k "=" v; next} {print}' "$ENV_FILE" >"$tmp"
		cat "$tmp" >"$ENV_FILE"
		rm -f "$tmp"
	else
		printf '%s=%s\n' "$key" "$value" >>"$ENV_FILE"
	fi
}

NEW_INSTALL=0
if [ ! -f "$ENV_FILE" ]; then
	NEW_INSTALL=1
	say "Writing $ENV_FILE"
	cat >"$ENV_FILE" <<EOF
# Acnologia settings, written by deploy/install-server.sh. Every variable is
# described in docs/configuration.md. After editing, run:
#   docker compose --project-directory $DIR up -d

# Encrypts every stored API key. Copy it to your password manager now:
# without it a backup of the database cannot be read.
ACNOLOGIA_MASTER_KEY=$(rand_b64 32)

# The lock screen in front of the whole site, and the owner's login.
ACNOLOGIA_ACCESS_PASSWORD=$(rand_pw)
ACNOLOGIA_REQUIRE_ACCESS_PASSWORD=true

# Turns on the Assistant, the provider builder and the offers refresh.
ANTHROPIC_API_KEY=$AI_KEY

ACNOLOGIA_PUBLIC_URL=$PUBLIC_URL
ACNOLOGIA_SECURE_COOKIES=false
ACNOLOGIA_TRUST_PROXY_HEADERS=false
EOF
fi
set_env ACNOLOGIA_PUBLIC_URL "$PUBLIC_URL"
if [ "$NO_TLS" = 1 ]; then
	# Behind your own proxy: SECURE_COOKIES and TRUST_PROXY_HEADERS are yours
	# to set in .env, and a rerun keeps them. Plain HTTP cannot carry Secure
	# cookies, though.
	case "$PUBLIC_URL" in http://*) set_env ACNOLOGIA_SECURE_COOKIES false ;; esac
else
	# Caddy terminates TLS and sets X-Forwarded-For itself.
	set_env ACNOLOGIA_SECURE_COOKIES true
	set_env ACNOLOGIA_TRUST_PROXY_HEADERS true
fi
if [ -n "$AI_KEY" ]; then
	set_env ANTHROPIC_API_KEY "$AI_KEY"
fi
chmod 600 "$ENV_FILE"

# 4. The compose file and the Caddyfile, rewritten on every run.
if [ "$BUILD" = 1 ]; then
	IMAGE="acnologia:local"
fi
write_compose() {
	cat <<EOF
# Written by deploy/install-server.sh; run the installer again rather than
# editing this file. Settings belong in .env.
name: acnologia
services:
  acnologia:
    image: $IMAGE
    restart: unless-stopped
    # Room for the 15s drain on SIGTERM; Docker's default is 10s.
    stop_grace_period: 20s
    env_file: .env
    environment:
      ACNOLOGIA_DATA_DIR: /data
      ACNOLOGIA_ADDR: ":8080"
    volumes:
      - ./data:/data
EOF
	if [ "$NO_TLS" = 1 ]; then
		cat <<EOF
    ports:
      - "$PORT:8080"
EOF
	else
		# Published on loopback only, for the health check and ssh tunnels;
		# the world reaches it through Caddy.
		cat <<EOF
    ports:
      - "127.0.0.1:$PORT:8080"
  caddy:
    image: caddy:2
    restart: unless-stopped
    depends_on: [acnologia]
    ports:
      - "80:80"
      - "443:443"
      - "443:443/udp"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - ./caddy/data:/data
      - ./caddy/config:/config
EOF
	fi
}
say "Writing $DIR/compose.yml"
write_compose >"$DIR/compose.yml"

if [ "$NO_TLS" != 1 ]; then
	{
		if [ -n "$EMAIL" ]; then
			printf '{\n\temail %s\n}\n\n' "$EMAIL"
		fi
		cat <<EOF
$DOMAIN {
	encode zstd gzip
	reverse_proxy acnologia:8080
}
EOF
	} >"$DIR/Caddyfile"
fi

# Open the ports when a host firewall is on.
open_ports() {
	local ports
	if [ "$NO_TLS" = 1 ]; then ports="$PORT/tcp"; else ports="80/tcp 443/tcp 443/udp"; fi
	if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
		for p in $ports; do ufw allow "$p" >/dev/null; done
		say "Opened $ports in ufw"
	elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
		for p in $ports; do firewall-cmd --permanent --add-port="$p" >/dev/null; done
		firewall-cmd --reload >/dev/null
		say "Opened $ports in firewalld"
	fi
}
open_ports

# 5. The image.
if [ "$BUILD" = 1 ]; then
	say "Building $IMAGE from $REPO_URL ($REF)"
	docker build -t "$IMAGE" "$REPO_URL.git#$REF"
else
	say "Pulling $IMAGE"
	if compose pull acnologia; then
		:
	elif docker image inspect "$IMAGE" >/dev/null 2>&1; then
		warn "could not pull $IMAGE; using the copy already on this server"
	else
		warn "could not pull $IMAGE; building it from source instead (a few minutes)"
		IMAGE="acnologia:local"
		write_compose >"$DIR/compose.yml"
		docker build -t "$IMAGE" "$REPO_URL.git#$REF"
	fi
fi

say "Starting"
compose up -d --remove-orphans

# 6. Wait for the server.
check_url="http://127.0.0.1:$PORT/healthz"
check() { curl -fsS --max-time 3 "$check_url" >/dev/null 2>&1; }
say "Waiting for the server"
ok=0
for _ in $(seq 1 60); do
	if check; then ok=1; break; fi
	sleep 2
done
if [ "$ok" != 1 ]; then
	compose logs --tail 50 acnologia || true
	die "the server did not become healthy ($check_url); the log is above"
fi

PASSWORD="$(grep '^ACNOLOGIA_ACCESS_PASSWORD=' "$ENV_FILE" | cut -d= -f2-)"
echo
say "Acnologia is running at $PUBLIC_URL"
if [ "$NO_TLS" != 1 ]; then
	echo "    The first visit can take up to a minute while Caddy gets the certificate."
fi
if [ "$NEW_INSTALL" = 1 ]; then
	echo
	echo "    Access password: $PASSWORD"
	echo
	echo "    It is also in $ENV_FILE, next to ACNOLOGIA_MASTER_KEY."
	echo "    Copy both to your password manager: the master key decrypts every"
	echo "    stored API key, and a backup is useless without it."
fi
echo
echo "    Upgrade:  run this installer again"
echo "    Logs:     docker compose --project-directory $DIR logs -f acnologia"
echo "    Backup:   see docs/deploy-vps.md#backups"
