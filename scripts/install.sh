#!/usr/bin/env bash
set -euo pipefail

# Build the airc executables the documented way and place them in a directory:
#
#   scripts/install.sh                         # aircd and airc into ~/.local/bin
#   scripts/install.sh airc-gateway            # the gateway, for --gateway
#   scripts/install.sh --output bin aircd airc
#
# Every binary is built with -trimpath -ldflags='-s -w' -tags grpcnotrace.
# -s -w drops the DWARF and symbol tables (about 31% of each file), so a
# debugger has no symbols, but panics still print function names and file:line.
# grpcnotrace stops gRPC importing golang.org/x/net/trace, which the gateway
# never enables; only airc-gateway links gRPC, so the tag changes no other
# binary. For a debugger, build without the -ldflags: go build -o DIR/ ./cmd/NAME
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
	names=(aircd airc)
fi

mkdir -p -- "$output"
output=$(cd -- "$output" && pwd -P)
packages=()
for name in "${names[@]}"; do
	packages+=("./cmd/$name")
done
source_dir=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
(cd -- "$source_dir" && go build -trimpath -ldflags='-s -w' -tags grpcnotrace -o "$output/" "${packages[@]}")
for name in "${names[@]}"; do
	printf '%s\n' "$output/$name"
done
