#!/usr/bin/env bash
set -euo pipefail

# Build the airc executables the way they are installed and report their size
# in Go benchmark format (one line per build, units bytes and gzip-bytes):
#
#   scripts/binary-size.sh --target linux/amd64 --target darwin/arm64 > new.txt
#   scripts/binary-size.sh -C ../airc-old > old.txt     # another checkout
#   scripts/binary-size.sh --compare old.txt new.txt     # size deltas
#
# Each binary is built three ways: plain (`go build`, no flags),
# trimpath-stripped (-trimpath -ldflags='-s -w') and installed (the build
# scripts/install.sh makes, so keep the flags in step). Installed added
# -tags grpcnotrace until no binary linked gRPC; it is now the same build as
# trimpath-stripped. Result files from before scripts/install.sh call the
# plain build "installed", as the docs then installed it, so --compare on
# "installed" follows what users get. gzip-bytes is the `gzip -9` size, a proxy for a
# download. airc-load is a development tool and is not installed. The host
# build uses the toolchain's default CGO_ENABLED, as an install would; cross
# builds use cgo 0. Builds are deterministic, so there is one sample per build
# and --compare prints exact deltas (benchstat reads the files but needs six
# samples a side before it shows a delta).
#
# --check also fails when an installed host binary exceeds its budget below.

usage() {
	printf 'usage: %s [-C SOURCE_DIRECTORY] [--target GOOS/GOARCH]... [--check]\n' "$0" >&2
	printf '       %s --compare OLD_RESULTS NEW_RESULTS\n' "$0" >&2
}

# Generous budgets (bytes) for the installed host build: about one and a half
# times the sizes of 12 October 2026 (airc 9.4 MB, aircd 6.7 MB, gateway
# 22.2 MB). They catch a new heavy dependency, not ordinary growth.
budget_for() {
	case "$1" in
		airc) echo $((14 * 1024 * 1024)) ;;
		aircd) echo $((10 * 1024 * 1024)) ;;
		airc-gateway) echo $((32 * 1024 * 1024)) ;;
		airc-load) echo $((10 * 1024 * 1024)) ;;
		*) echo 0 ;;
	esac
}

compare() {
	awk '
		function key(name) { return goos "/" goarch " cgo=" cgo " " name }
		BEGIN { printf "%-50s %11s %11s %8s %11s %11s %8s\n", "target build", "old bytes", "new bytes", "delta", "old gzip", "new gzip", "delta" }
		/^goos: / { goos = $2 }
		/^goarch: / { goarch = $2 }
		/^cgo: / { cgo = $2 }
		/^BenchmarkBinarySize\// {
			k = key(substr($1, length("BenchmarkBinarySize/") + 1))
			if (FILENAME == ARGV[1]) {
				oldb[k] = $3
				oldg[k] = $5
				next
			}
			if (!(k in oldb)) {
				printf "%-50s %11s %11d %8s %11s %11d %8s\n", k, "-", $3, "new", "-", $5, "new"
				next
			}
			printf "%-50s %11d %11d %+7.1f%% %11d %11d %+7.1f%%\n", k, oldb[k], $3, 100 * ($3 - oldb[k]) / oldb[k], oldg[k], $5, 100 * ($5 - oldg[k]) / oldg[k]
		}
	' "$1" "$2"
}

script_dir=$(cd -- "$(dirname -- "$0")" && pwd -P)
source_dir="$script_dir/.."
targets=()
check=0
while (( $# > 0 )); do
	case "$1" in
		-C)
			if (( $# < 2 )); then
				usage
				exit 2
			fi
			source_dir=$2
			shift 2
			;;
		--target)
			if (( $# < 2 )) || [[ "$2" != */* ]]; then
				usage
				exit 2
			fi
			targets+=("$2")
			shift 2
			;;
		--check)
			check=1
			shift
			;;
		--compare)
			if (( $# != 3 )); then
				usage
				exit 2
			fi
			compare "$2" "$3"
			exit 0
			;;
		*)
			usage
			exit 2
			;;
	esac
done
source_dir=$(cd -- "$source_dir" && pwd -P)
host="$(go env GOHOSTOS)/$(go env GOHOSTARCH)"
if (( ${#targets[@]} == 0 )); then
	targets=("$host")
fi

# airc-gateway is its own Go module, gateway/, built from there (an older
# checkout holds it in the root module's cmd/, which -C still measures).
bins=()
packages=()
gateway_packages=()
for bin in airc aircd airc-gateway airc-load; do
	if [[ -d "$source_dir/cmd/$bin" ]]; then
		bins+=("$bin")
		packages+=("./cmd/$bin")
	elif [[ -d "$source_dir/gateway/cmd/$bin" ]]; then
		bins+=("$bin")
		gateway_packages+=("./cmd/$bin")
	fi
done

work=$(mktemp -d "${TMPDIR:-/tmp}/airc-size.XXXXXX")
cleanup() { rm -rf -- "$work"; }
trap cleanup EXIT

failed=0
# Comments, not "key: value" lines, so benchstat does not split tables by commit.
printf '# commit %s\n' "$(git -C "$source_dir" rev-parse --short HEAD 2>/dev/null || echo unknown)"
printf '# go %s\n' "$(cd -- "$source_dir" && go env GOVERSION)"
for target in "${targets[@]}"; do
	goos=${target%/*}
	goarch=${target#*/}
	cgo=$(cd -- "$source_dir" && GOOS=$goos GOARCH=$goarch go env CGO_ENABLED)
	printf 'goos: %s\ngoarch: %s\ncgo: %s\npkg: github.com/Someblueman/airc/cmd\n' "$goos" "$goarch" "$cgo"
	for variant in plain trimpath-stripped installed; do
		case "$variant" in
			plain) flags=() ;;
			trimpath-stripped) flags=(-trimpath "-ldflags=-s -w") ;;
			installed) flags=(-trimpath "-ldflags=-s -w") ;;
		esac
		out="$work/$goos-$goarch-$variant"
		mkdir -p -- "$out"
		(cd -- "$source_dir" && GOOS=$goos GOARCH=$goarch go build ${flags[@]+"${flags[@]}"} -o "$out/" "${packages[@]}")
		if (( ${#gateway_packages[@]} > 0 )); then
			(cd -- "$source_dir/gateway" && GOOS=$goos GOARCH=$goarch go build ${flags[@]+"${flags[@]}"} -o "$out/" "${gateway_packages[@]}")
		fi
		for bin in "${bins[@]}"; do
			bytes=$(wc -c < "$out/$bin")
			gz=$(gzip -9 -n -c -- "$out/$bin" | wc -c)
			printf 'BenchmarkBinarySize/%s/%s 1 %d bytes %d gzip-bytes\n' "$bin" "$variant" "$bytes" "$gz"
			if (( check == 1 )) && [[ "$target" == "$host" && "$variant" == installed ]]; then
				budget=$(budget_for "$bin")
				if (( budget > 0 && bytes > budget )); then
					printf 'error: %s is %d bytes, over its %d-byte budget\n' "$bin" "$bytes" "$budget" >&2
					failed=1
				fi
			fi
		done
		rm -rf -- "$out"
	done
done
exit "$failed"
