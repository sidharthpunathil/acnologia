#!/usr/bin/env bash
# Prints cloud-init user data that installs Acnologia on first boot. Shared
# by hetzner.sh and digitalocean.sh; usable on its own for any other cloud:
#
#   deploy/lib/user-data.sh --domain key.example.com > user-data.yaml
#
# Same options as the installer's --domain, --email and --anthropic-key; any
# other option is passed through to install-server.sh as is.
set -euo pipefail

INSTALLER_URL="${ACN_INSTALLER_URL:-https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/deploy/install-server.sh}"

args=()
while [ $# -gt 0 ]; do
	case "$1" in
	--domain | --email | --anthropic-key | --image | --ref | --port | --dir)
		args+=("$1" "${2:?$1 needs a value}")
		shift 2
		;;
	*)
		args+=("$1")
		shift
		;;
	esac
done

# Each argument single quoted for the shell cloud-init runs.
quoted=""
for a in ${args[@]+"${args[@]}"}; do
	quoted="$quoted '$(printf '%s' "$a" | sed "s/'/'\\\\''/g")'"
done

cat <<EOF
#cloud-config
# Installs Acnologia on first boot. Progress: tail -f /var/log/acnologia-install.log
runcmd:
  - |
    set -e
    exec >>/var/log/acnologia-install.log 2>&1
    curl -fsSL $INSTALLER_URL -o /root/acnologia-install.sh
    bash /root/acnologia-install.sh$quoted
EOF
