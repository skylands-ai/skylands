#!/usr/bin/env bash
# Writes the licenses of the third-party code in the skylands binary: Go
# itself, the Go modules it's built from on Linux and macOS, and the npm
# packages the web client bundles, fonts included. The release puts the file
# in every archive.
#
# Usage: scripts/third-party-notices.sh [output]
#
# The output defaults to THIRD_PARTY_NOTICES. The web client's packages must
# be installed, as `make web` does. A Go module without a license file fails
# the script, so a new dependency can't ship without its notice.
set -euo pipefail

cd "$(dirname "$0")/.."
out=${1:-THIRD_PARTY_NOTICES}
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
missing=0

rule() { printf '%.0s=' {1..80}; printf '\n'; }

# section prints a heading for name, then each license file found in dir.
# It reports false when dir has none.
section() {
	local name=$1 dir=$2 files
	files=$(find "$dir" -maxdepth 1 -type f \( -iname 'license*' -o -iname 'licence*' -o -iname 'copying*' -o -iname 'notice*' \) | sort)
	[ -n "$files" ] || return 1
	{
		rule
		printf '%s\n' "$name"
		rule
		while IFS= read -r f; do
			printf '\n'
			cat "$f"
			printf '\n'
		done <<<"$files"
		printf '\n'
	} >>"$tmp"
}

{
	printf 'Third-party notices for skylands\n\n'
	printf 'The skylands binary contains the software below. Each part is used under\n'
	printf 'the license that follows its name.\n\n'
} >"$tmp"

goroot=$(go env GOROOT)
section "Go $(go env GOVERSION), the standard library and runtime" "$goroot"

modules=$(for os in linux darwin; do
	GOOS=$os go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}} {{.Dir}}{{end}}{{end}}' ./cmd/skylands
done | sort -u)
while read -r mod dir; do
	[ -n "$mod" ] || continue
	if ! section "$mod" "$dir"; then
		echo "third-party-notices: no license file for $mod in $dir" >&2
		missing=1
	fi
done <<<"$modules"

# npm ls lists the web client itself first, then every package it bundles.
packages=$(cd web && npm ls --omit=dev --all --parseable | tail -n +2 | sort -u)
while read -r dir; do
	[ -n "$dir" ] || continue
	pkg=$(node -e 'const p = require(process.argv[1] + "/package.json"); console.log(p.name + "@" + p.version + " " + (p.license || ""))' "$dir")
	if ! section "${pkg% *} (npm)" "$dir"; then
		# A package without a license file still names its license.
		{
			rule
			printf '%s (npm)\n' "${pkg% *}"
			rule
			printf '\nLicensed under %s, as its package.json says. It ships no license file.\n\n' "${pkg##* }"
		} >>"$tmp"
	fi
done <<<"$packages"

if [ "$missing" -ne 0 ]; then
	exit 1
fi
mv "$tmp" "$out"
trap - EXIT
echo "third-party-notices: wrote $out"
