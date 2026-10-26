#!/usr/bin/env bash
set -euo pipefail

# Build the airc executables the documented way and place them in a directory:
#
#   scripts/install.sh                         # aircd, airc and airc-gateway into ~/.local/bin
#   scripts/install.sh airc                    # just the CLI
#   scripts/install.sh --output bin aircd airc
#
# Every binary is built with -trimpath -ldflags='-s -w'. -s -w drops the DWARF
# and symbol tables (about 31% of each file), so a debugger has no symbols, but
# panics still print function names and file:line. For a debugger, build
# without the -ldflags: go build -o DIR/ ./cmd/NAME
# scripts/binary-size.sh measures this same build; keep its flags in step.

usage() {
	printf 'usage: %s [--output DIRECTORY] [aircd|airc|airc-gateway|airc-load]...\n' "$0" >&2
}

output=${HOME:?}/.local/bin
names=()
while (( $# > 0 )); do
	case "$1" in
		--output)
			if (( $# < 2 )); then
				usage
				exit 2
			fi
			output=$2
			shift 2
			;;
		aircd | airc | airc-gateway | airc-load)
			names+=("$1")
			shift
			;;
		*)
			usage
			exit 2
			;;
	esac
done
if (( ${#names[@]} == 0 )); then
	names=(aircd airc airc-gateway)
fi

mkdir -p -- "$output"
output=$(cd -- "$output" && pwd -P)
# airc-gateway is its own Go module (gateway/), built from there; the rest
# are the root module's.
packages=()
gateway=0
for name in "${names[@]}"; do
	if [[ "$name" == airc-gateway ]]; then
		gateway=1
	else
		packages+=("./cmd/$name")
	fi
done
source_dir=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
if (( ${#packages[@]} > 0 )); then
	(cd -- "$source_dir" && go build -trimpath -ldflags='-s -w' -o "$output/" "${packages[@]}")
fi
if (( gateway == 1 )); then
	(cd -- "$source_dir/gateway" && go build -trimpath -ldflags='-s -w' -o "$output/" ./cmd/airc-gateway)
fi
for name in "${names[@]}"; do
	printf '%s\n' "$output/$name"
done
