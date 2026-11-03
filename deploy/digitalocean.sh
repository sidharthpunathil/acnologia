#!/usr/bin/env bash
# Creates a DigitalOcean Droplet running Acnologia, from your machine, with
# doctl (brew install doctl, or https://docs.digitalocean.com/reference/doctl/).
#
#   doctl auth init                        # paste an API token, read and write
#   deploy/digitalocean.sh --domain key.example.com --email you@example.com
#
# App Platform has no persistent volumes, so Acnologia runs on a Droplet. This
# adds a cloud firewall (22, 80, 443), uploads your SSH key if DigitalOcean
# does not have it yet, creates the Droplet with cloud-init user data that runs
# deploy/install-server.sh, turns on weekly Droplet backups, and waits until
# the site answers.
#
# Options:
#   --name NAME          Droplet name (default acnologia)
#   --size SLUG          default s-1vcpu-1gb ($6 a month); s-1vcpu-512mb-10gb
#                        ($4) also runs it
#   --region SLUG        nyc3, sfo3, ams3, fra1, lon1, sgp1, blr1, syd1, tor1
#                        (default nyc3)
#   --no-backups         skip Droplet backups (they add 20% to the price)
#   --ssh-key PATH       public key to log in with (default ~/.ssh/id_ed25519.pub)
#   --domain, --email, --anthropic-key   passed to the installer
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=deploy/lib/provision.sh
. "$here/lib/provision.sh"

NAME=acnologia
SIZE="${DO_SIZE:-s-1vcpu-1gb}"
REGION="${DO_REGION:-nyc3}"
IMAGE=ubuntu-24-04-x64
BACKUPS=1
parse_common_args "$@"
set -- "${REST[@]+"${REST[@]}"}"
while [ $# -gt 0 ]; do
	case "$1" in
	--size) SIZE="${2:?}"; shift 2 ;;
	--region) REGION="${2:?}"; shift 2 ;;
	--no-backups) BACKUPS=0; shift ;;
	*) die "unknown option: $1" ;;
	esac
done

need doctl "install it with 'brew install doctl' or see https://docs.digitalocean.com/reference/doctl/how-to/install/"
doctl account get >/dev/null 2>&1 || die "doctl is not signed in; run: doctl auth init"
if doctl compute droplet list --format Name --no-header | grep -qx "$NAME"; then
	die "a Droplet named $NAME already exists; pass --name, or rerun the installer on it to upgrade"
fi

fp="$(ssh_key_fingerprint)"
if ! doctl compute ssh-key list --format FingerPrint --no-header | grep -qx "$fp"; then
	say "Uploading $SSH_KEY to DigitalOcean"
	doctl compute ssh-key import "acnologia-$(echo "$fp" | tr -d ':' | cut -c1-12)" --public-key-file "$SSH_KEY" >/dev/null
fi

user_data="$(mktemp)"
trap 'rm -f "$user_data"' EXIT
write_user_data >"$user_data"

backup_flag=()
[ "$BACKUPS" = 1 ] && backup_flag=(--enable-backups)

say "Creating $SIZE Droplet $NAME in $REGION"
droplet_id="$(doctl compute droplet create "$NAME" --image "$IMAGE" --size "$SIZE" --region "$REGION" \
	--ssh-keys "$fp" --user-data-file "$user_data" --tag-names acnologia \
	--enable-monitoring ${backup_flag[@]+"${backup_flag[@]}"} --wait --format ID --no-header)"

fw="$NAME-web"
if ! doctl compute firewall list --format Name --no-header | grep -qx "$fw"; then
	say "Creating cloud firewall $fw (ssh, http, https)"
	doctl compute firewall create --name "$fw" --tag-names acnologia \
		--inbound-rules "protocol:tcp,ports:22,address:0.0.0.0/0,address:::/0 protocol:tcp,ports:80,address:0.0.0.0/0,address:::/0 protocol:tcp,ports:443,address:0.0.0.0/0,address:::/0 protocol:udp,ports:443,address:0.0.0.0/0,address:::/0" \
		--outbound-rules "protocol:tcp,ports:all,address:0.0.0.0/0,address:::/0 protocol:udp,ports:all,address:0.0.0.0/0,address:::/0 protocol:icmp,address:0.0.0.0/0,address:::/0" >/dev/null
fi

ip="$(doctl compute droplet get "$droplet_id" --format PublicIPv4 --no-header)"
finish "$ip"
