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
	printf 'error: D-Bus bridge fixture must be built on Darwin ARM64\n' >&2
	exit 1
fi
if ! command -v pkg-config >/dev/null 2>&1 || ! pkg-config --exists gio-2.0 gio-unix-2.0; then
	printf 'error: GLib/GIO development files are required (gio-2.0, gio-unix-2.0)\n' >&2
	exit 1
fi

case "$output_arg" in
	/*) output_path=$output_arg ;;
	*) output_path="$PWD/$output_arg" ;;
esac
output_parent=$(dirname -- "$output_path")
output_name=$(basename -- "$output_path")
mkdir -p -- "$output_parent"
output_parent=$(cd -- "$output_parent" && pwd)
output_path="$output_parent/$output_name"
if [[ -e "$output_path" || -L "$output_path" ]]; then
	printf 'error: output already exists: %s\n' "$output_path" >&2
	exit 1
fi

script_dir=$(cd -- "$(dirname -- "$0")" && pwd)
native_dir="$script_dir/../native"
for source in dbus/client.c dbus/server.c dbus/session.c dbus/session_filter.c mach/client.c mach/client.h; do
	if [[ ! -f "$native_dir/$source" ]]; then
		printf 'error: missing native source: %s\n' "$native_dir/$source" >&2
		exit 1
	fi
done

sdk_path=$(xcrun --sdk macosx --show-sdk-path)
compiler=("$(xcrun --sdk macosx --find clang)" -std=c11 -O1 -g -Wall -Wextra -Werror -pedantic -arch arm64 -isysroot "$sdk_path")
if (( sanitize != 0 )); then
	compiler+=("-fsanitize=address,undefined" -fno-sanitize-recover=all -fno-omit-frame-pointer)
fi
read -r -a gio_cflags <<< "$(pkg-config --cflags gio-2.0 gio-unix-2.0)"
read -r -a gio_libs <<< "$(pkg-config --libs gio-2.0 gio-unix-2.0)"

build_root=$(mktemp -d "$output_parent/.dbus-bridge.XXXXXX")
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

"${compiler[@]}" "${gio_cflags[@]}" "$native_dir/dbus/client.c" "${gio_libs[@]}" -o "$build_root/dbus-client"
"${compiler[@]}" "${gio_cflags[@]}" -I"$native_dir/dbus" -I"$native_dir/mach" \
	"$native_dir/dbus/server.c" "$native_dir/dbus/session.c" "$native_dir/mach/client.c" \
	"$native_dir/dbus/session_filter.c" \
	"${gio_libs[@]}" -o "$build_root/dbus-server"

mkdir -- "$output_path"
output_created=1
mv -- "$build_root/dbus-client" "$output_path/dbus-client"
mv -- "$build_root/dbus-server" "$output_path/dbus-server"
output_created=0
printf 'built D-Bus bridge: %s\n' "$output_path"
