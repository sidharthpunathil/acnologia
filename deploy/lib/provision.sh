# shellcheck shell=bash disable=SC2034
# Shared by the provider scripts (hetzner.sh, digitalocean.sh). Sourced, not
# run. Sets NAME, SSH_KEY, DOMAIN, INSTALL_ARGS and REST (the options the
# calling script handles itself).

say() { printf '\033[1m==>\033[0m %s\n' "$*"; }
die() { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "$1 is not installed: $2"; }

LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SSH_KEY=""
DOMAIN=""
INSTALL_ARGS=()
REST=()

parse_common_args() {
	while [ $# -gt 0 ]; do
		case "$1" in
		--name) NAME="${2:?--name needs a value}"; shift 2 ;;
		--ssh-key) SSH_KEY="${2:?--ssh-key needs a value}"; shift 2 ;;
		--domain) DOMAIN="${2:?--domain needs a value}"; INSTALL_ARGS+=(--domain "$2"); shift 2 ;;
		--email | --anthropic-key | --image | --ref)
			INSTALL_ARGS+=("$1" "${2:?$1 needs a value}")
			shift 2
			;;
		-h | --help)
			sed -n '2,/^set -euo/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'
			exit 0
			;;
		*) REST+=("$1"); shift ;;
		esac
	done
	if [ -z "$SSH_KEY" ]; then
		for k in "$HOME/.ssh/id_ed25519.pub" "$HOME/.ssh/id_ecdsa.pub" "$HOME/.ssh/id_rsa.pub"; do
			if [ -f "$k" ]; then SSH_KEY="$k"; break; fi
		done
	fi
	[ -n "$SSH_KEY" ] && [ -f "$SSH_KEY" ] || die "no SSH public key found; make one with ssh-keygen -t ed25519, or pass --ssh-key"
	need ssh-keygen "it ships with OpenSSH"
	need curl "install curl"
}

# MD5 fingerprint (aa:bb:...), the form Hetzner and DigitalOcean list keys by.
ssh_key_fingerprint() { ssh-keygen -E md5 -lf "$SSH_KEY" | awk '{print $2}' | sed 's/^MD5://'; }

write_user_data() { "$LIB_DIR/user-data.sh" ${INSTALL_ARGS[@]+"${INSTALL_ARGS[@]}"}; }

# Waits for the new server to answer over HTTPS, then prints where it is and
# how to read the access password.
finish() {
	local ip="$1" host url
	if [ -n "$DOMAIN" ]; then
		host="$DOMAIN"
		say "Server is up at $ip. Point an A record for $DOMAIN at $ip now,"
		say "if you have not: the certificate is issued once DNS resolves."
	else
		host="$(echo "$ip" | tr . -).sslip.io"
	fi
	url="https://$host"
	say "Installing (about 2 to 4 minutes). Waiting for $url"
	for _ in $(seq 1 90); do
		if curl -fsS --max-time 5 -o /dev/null "$url/healthz" 2>/dev/null; then
			echo
			say "Acnologia is running at $url"
			echo "    Access password:  ssh root@$ip grep ACCESS_PASSWORD /opt/acnologia/.env"
			echo "    Back up the key:  ssh root@$ip grep MASTER_KEY /opt/acnologia/.env"
			echo "    Upgrade later:    ssh root@$ip bash /root/acnologia-install.sh"
			return 0
		fi
		sleep 5
	done
	echo
	say "Not answering yet. Check the install log:"
	echo "    ssh root@$ip tail -n 50 /var/log/acnologia-install.log"
	return 1
}
