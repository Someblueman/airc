#!/usr/bin/env bash
set -euo pipefail
umask 077

usage() {
	printf 'usage: %s --output FRESH_DIRECTORY [--sanitize]\n' "$0" >&2
}

output_arg=
sanitize=0
while (( $# > 0 )); do
	case "$1" in
		--output)
			if (( $# < 2 )) || [[ -n "$output_arg" ]]; then
				usage
				exit 2
			fi
			output_arg=$2
			shift 2
			;;
		--sanitize)
			if (( sanitize != 0 )); then
				usage
				exit 2
			fi
			sanitize=1
			shift
			;;
		*)
			usage
			exit 2
			;;
	esac
done

if [[ -z "$output_arg" ]]; then
	usage
	exit 2
fi
if [[ "$(uname -s)" != Darwin || "$(uname -m)" != arm64 ]]; then
	printf 'error: QR tool must be built on Darwin ARM64\n' >&2
	exit 1
fi

case "$output_arg" in
	/*) output_path=$output_arg ;;
	*) output_path="$PWD/$output_arg" ;;
esac
output_parent=$(dirname -- "$output_path")
output_name=$(basename -- "$output_path")
mkdir -p -- "$output_parent"
output_parent=$(cd -- "$output_parent" && pwd -P)
output_path="$output_parent/$output_name"
if [[ -e "$output_path" || -L "$output_path" ]]; then
	printf 'error: output already exists: %s\n' "$output_path" >&2
	exit 1
fi

script_dir=$(cd -- "$(dirname -- "$0")" && pwd -P)
source_file="$script_dir/../native/qr/main.swift"
camera_source="$script_dir/../native/qr/camera.swift"
info_plist="$script_dir/../native/qr/Info.plist"
if [[ ! -f "$source_file" || ! -f "$camera_source" || ! -f "$info_plist" ]]; then
	printf 'error: missing QR codec source or camera metadata\n' >&2
	exit 1
fi

sdk_path=$(xcrun --sdk macosx --show-sdk-path)
compiler=("$(xcrun --sdk macosx --find swiftc)" -O -warnings-as-errors -target arm64-apple-macos13.0 -sdk "$sdk_path")
if (( sanitize != 0 )); then
	compiler+=("-sanitize=address")
fi

build_root=$(mktemp -d "$output_parent/.qr-tool.XXXXXX")
output_created=0
cleanup() {
	if (( output_created != 0 )); then
		rm -rf -- "$output_path"
	fi
	rm -rf -- "$build_root"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

"${compiler[@]}" -module-cache-path "$build_root/modules" "$source_file" "$camera_source" \
	-Xlinker -sectcreate -Xlinker __TEXT -Xlinker __info_plist -Xlinker "$info_plist" \
	-o "$build_root/qr-tool"
mkdir -- "$output_path"
output_created=1
chmod 0700 "$output_path"
mv -- "$build_root/qr-tool" "$output_path/qr-tool"
chmod 0700 "$output_path/qr-tool"
output_created=0
printf 'built QR tool: %s/qr-tool\n' "$output_path"
