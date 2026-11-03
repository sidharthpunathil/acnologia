#!/usr/bin/env bash
# Creates a Hetzner Cloud server running Acnologia, from your machine, with
# the hcloud CLI (brew install hcloud, or https://github.com/hetznercloud/cli).
#
#   hcloud context create acnologia        # paste an API token, read and write
#   deploy/hetzner.sh --domain key.example.com --email you@example.com
#
# It adds a firewall (22, 80, 443), uploads your SSH key if Hetzner does not
# have it yet, creates the server with cloud-init user data that runs
# deploy/install-server.sh, and waits until the site answers.
#
# Options:
#   --name NAME          server name (default acnologia)
#   --type TYPE          server type. Default: the first of cx23 (x86),
#                        cax11 (Arm) and cpx12 that Hetzner has in stock
#                        at --location
#   --location LOC       fsn1, nbg1, hel1, ash, hil or sin (default fsn1)
#   --ssh-key PATH       public key to log in with (default the first of
#                        ~/.ssh/id_ed25519.pub, id_ecdsa.pub, id_rsa.pub)
#   --domain, --email, --anthropic-key   passed to the installer
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=deploy/lib/provision.sh
. "$here/lib/provision.sh"

NAME=acnologia
TYPE="${HCLOUD_SERVER_TYPE:-}"
LOCATION="${HCLOUD_LOCATION:-fsn1}"
IMAGE=ubuntu-24.04
parse_common_args "$@"
set -- "${REST[@]+"${REST[@]}"}"
while [ $# -gt 0 ]; do
	case "$1" in
	--type) TYPE="${2:?}"; shift 2 ;;
	--location) LOCATION="${2:?}"; shift 2 ;;
	*) die "unknown option: $1" ;;
	esac
done

need hcloud "install it with 'brew install hcloud' or from https://github.com/hetznercloud/cli"
hcloud server list >/dev/null 2>&1 || die "hcloud is not signed in; run: hcloud context create acnologia"
hcloud server describe "$NAME" >/dev/null 2>&1 && die "a server named $NAME already exists; pass --name, or rerun the installer on it to upgrade"

key_name="acnologia-$(ssh_key_fingerprint | tr -d ':' | cut -c1-12)"
if ! hcloud ssh-key list -o noheader -o columns=fingerprint | grep -qx "$(ssh_key_fingerprint)"; then
	say "Uploading $SSH_KEY to Hetzner as $key_name"
	hcloud ssh-key create --name "$key_name" --public-key-from-file "$SSH_KEY" >/dev/null
fi
key_id="$(hcloud ssh-key list -o noheader -o columns=id,fingerprint | awk -v f="$(ssh_key_fingerprint)" '$2==f {print $1; exit}')"

fw="$NAME-web"
if ! hcloud firewall describe "$fw" >/dev/null 2>&1; then
	say "Creating firewall $fw (ssh, http, https)"
	hcloud firewall create --name "$fw" >/dev/null
	for p in 22 80 443; do
		hcloud firewall add-rule "$fw" --direction in --protocol tcp --port "$p" \
			--source-ips 0.0.0.0/0 --source-ips ::/0 >/dev/null
	done
	hcloud firewall add-rule "$fw" --direction in --protocol udp --port 443 \
		--source-ips 0.0.0.0/0 --source-ips ::/0 >/dev/null
fi

user_data="$(mktemp)"
trap 'rm -f "$user_data"' EXIT
write_user_data >"$user_data"

# The cheap types sell out now and then, so without --type try each in turn.
candidates="${TYPE:-cx23 cax11 cpx12}"
created=""
for t in $candidates; do
	say "Creating $t server $NAME in $LOCATION"
	if err="$(hcloud server create --name "$NAME" --type "$t" --image "$IMAGE" --location "$LOCATION" \
		--ssh-key "$key_id" --firewall "$fw" --user-data-from-file "$user_data" 2>&1 >/dev/null)"; then
		created="$t"
		break
	fi
	printf '%s\n' "$err" >&2
	# Only a sold out or retired type moves on to the next one.
	if [ -n "$TYPE" ] || ! printf '%s' "$err" | grep -qiE 'unavailable|not available|deprecated|unsupported|sold out|invalid.*server type|server type.*not found'; then
		break
	fi
	say "$t is not available here, trying the next type"
done
[ -n "$created" ] || die "could not create the server; try another --location or --type (hcloud server-type list)"

ip="$(hcloud server ip "$NAME")"
finish "$ip"
