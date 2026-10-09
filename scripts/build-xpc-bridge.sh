#!/usr/bin/env bash
set -euo pipefail
umask 077

usage() {
	printf 'usage: %s --output PATH.app --mach-service NAME [--sanitize]\n' "$0" >&2
}

output_arg=
mach_service=
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
		--mach-service)
			if (( $# < 2 )) || [[ -n "$mach_service" ]]; then
				usage
				exit 2
			fi
			mach_service=$2
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

if [[ -z "$output_arg" || -z "$mach_service" || ! "$output_arg" == *.app || ! "$mach_service" =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]{0,126}$ ]]; then
	usage
	printf 'error: output must end in .app and mach service name must be valid\n' >&2
	exit 2
fi
if [[ "$(uname -s)" != Darwin || "$(uname -m)" != arm64 ]]; then
	printf 'error: XPC bridge must be built on Darwin ARM64\n' >&2
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
native_dir="$script_dir/../native/xpc"
mach_dir="$script_dir/../native/mach"
if [[ ! -f "$native_dir/client.c" || ! -f "$native_dir/bridge.c" || ! -f "$mach_dir/client.c" || ! -f "$mach_dir/client.h" ]]; then
	printf 'error: XPC bridge native sources are incomplete\n' >&2
	exit 1
fi

build_root=$(mktemp -d "$output_parent/.xpc-bridge.XXXXXX")
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

app="$build_root/$output_name"
client_dir="$app/Contents/MacOS"
service_bundle="$app/Contents/XPCServices/org.airc.gateway.xpc.bridge.xpc"
service_dir="$service_bundle/Contents/MacOS"
mkdir -p -- "$client_dir" "$service_dir"

cat > "$app/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key><string>client</string>
	<key>CFBundleIdentifier</key><string>org.airc.gateway.xpc.client</string>
	<key>CFBundleName</key><string>AIRC XPC Client</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>1</string>
	<key>CFBundleVersion</key><string>1</string>
	<key>LSMinimumSystemVersion</key><string>13.0</string>
</dict>
</plist>
PLIST

cat > "$service_bundle/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>AIRCUpstreamMachService</key><string>$mach_service</string>
	<key>CFBundleExecutable</key><string>bridge</string>
	<key>CFBundleIdentifier</key><string>org.airc.gateway.xpc.bridge</string>
	<key>CFBundleName</key><string>AIRC XPC Bridge</string>
	<key>CFBundlePackageType</key><string>XPC!</string>
	<key>CFBundleShortVersionString</key><string>1</string>
	<key>CFBundleVersion</key><string>1</string>
	<key>XPCService</key>
	<dict><key>ServiceType</key><string>Application</string></dict>
</dict>
</plist>
PLIST

compiler=(xcrun --sdk macosx clang -std=c11 -O1 -g -fblocks -Wall -Wextra -Werror -pedantic -target arm64-apple-macos13.0 -isysroot "$(xcrun --sdk macosx --show-sdk-path)")
if (( sanitize != 0 )); then
	compiler+=("-fsanitize=address,undefined" -fno-sanitize-recover=all -fno-omit-frame-pointer)
fi
"${compiler[@]}" "$native_dir/client.c" -framework CoreFoundation -o "$client_dir/client"
"${compiler[@]}" "$native_dir/bridge.c" "$mach_dir/client.c" -framework CoreFoundation -o "$service_dir/bridge"

codesign --force --sign - "$service_bundle"
codesign --force --sign - "$app"
codesign --verify --strict --verbose=2 "$service_bundle"
codesign --verify --deep --strict --verbose=2 "$app"

mkdir -- "$output_path"
output_created=1
mv -- "$app/Contents" "$output_path/Contents"
output_created=0
rmdir -- "$app"
printf 'built XPC bridge: %s\n' "$output_path"
