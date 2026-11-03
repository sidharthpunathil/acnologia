#!/bin/sh
# Installs acn, the Acnologia command line client.
#
#   curl -fsSL https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/install.sh | sh
#
# It downloads the newest cli-v* release for this OS and CPU, checks it
# against the release's SHA256SUMS, and puts acn in /usr/local/bin when that
# is writable, else in ~/.local/bin. It never calls sudo; run it with sudo
# yourself if you want it somewhere only root can write.
#
# ACN_VERSION=cli-v0.1.0 picks a release, ACN_INSTALL_DIR=/some/bin a place.
# On Windows use install.ps1 from PowerShell:
#
#   irm https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/install.ps1 | iex
set -eu

REPO="sidharthpunathil/acnologia"

say() { printf '%s\n' "$*"; }
fail() {
	printf 'acn install: %s\n' "$*" >&2
	exit 1
}

case "$(uname -s)" in
Darwin) os=darwin ;;
Linux) os=linux ;;
MINGW* | MSYS* | CYGWIN* | Windows_NT) fail "this script handles macOS and Linux; on Windows run this in PowerShell instead: irm https://raw.githubusercontent.com/$REPO/main/install.ps1 | iex" ;;
*) fail "no acn build for $(uname -s); see https://github.com/$REPO/releases" ;;
esac

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) fail "no acn build for $(uname -m)" ;;
esac

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q -O "$2" "$1"; }
else
	fail "needs curl or wget"
fi

if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
else
	fail "needs sha256sum or shasum to check the download"
fi

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t acn)
trap 'rm -rf "$tmp"' EXIT INT TERM

tag="${ACN_VERSION:-}"
if [ -z "$tag" ]; then
	fetch "https://api.github.com/repos/$REPO/releases?per_page=100" "$tmp/releases.json" ||
		fail "could not list the releases of $REPO"
	tag=$(grep -o '"tag_name": *"cli-v[^"]*"' "$tmp/releases.json" | head -n 1 | sed 's/.*"\(cli-v[^"]*\)"$/\1/')
	[ -n "$tag" ] || fail "no cli-v release found in $REPO"
fi

asset="acn_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"
say "Downloading acn $tag for $os/$arch"
fetch "$base/$asset" "$tmp/$asset" || fail "could not download $base/$asset"
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || fail "could not download $base/SHA256SUMS"

want=$(awk -v a="$asset" '$2 == a || $2 == "*" a {print $1}' "$tmp/SHA256SUMS")
[ -n "$want" ] || fail "$asset is not listed in SHA256SUMS"
got=$(sha256 "$tmp/$asset")
[ "$want" = "$got" ] || fail "checksum mismatch for $asset (want $want, got $got); not installing"

mkdir "$tmp/x"
tar -xzf "$tmp/$asset" -C "$tmp/x"
[ -f "$tmp/x/acn" ] || fail "the archive has no acn binary"

dest="${ACN_INSTALL_DIR:-}"
if [ -z "$dest" ]; then
	if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
		dest=/usr/local/bin
	else
		[ -n "${HOME:-}" ] || fail "HOME is not set; pass ACN_INSTALL_DIR"
		dest="$HOME/.local/bin"
	fi
fi
mkdir -p "$dest" || fail "cannot create $dest"
[ -w "$dest" ] || fail "cannot write to $dest; pick another with ACN_INSTALL_DIR, or run this with sudo"

cp "$tmp/x/acn" "$dest/.acn.new"
chmod 0755 "$dest/.acn.new"
mv -f "$dest/.acn.new" "$dest/acn"

say "Installed $("$dest/acn" version 2>/dev/null || echo acn) to $dest/acn"
case ":${PATH:-}:" in
*":$dest:"*) ;;
*)
	say ""
	say "$dest is not on your PATH. Add it, for example:"
	say "  echo 'export PATH=\"$dest:\$PATH\"' >> ~/.profile"
	;;
esac
say ""
say "Next: acn login"
