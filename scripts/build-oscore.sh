#!/usr/bin/env bash
set -euo pipefail

# Archive the exact upstream commit, ignoring local edits in the source checkout.
# The output is a new build directory; this script never installs anything.
if [[ $# != 2 ]]; then
  printf 'usage: %s LIBCOAP_GIT_REPOSITORY NEW_OUTPUT_DIRECTORY\n' "$0" >&2
  exit 2
fi
revision=75a532c1f7fcb3053f91c8f30ac2f49355194028
script_dir=$(cd -- "$(dirname -- "$0")" && pwd)
native_dir="$script_dir/../native/oscore"
source_repo=$(cd -- "$1" && pwd)
git -C "$source_repo" cat-file -e "$revision^{commit}"
mkdir -- "$2"
output=$(cd -- "$2" && pwd)
scratch=$(mktemp -d "$output/.archive.XXXXXX")
cleanup() { rm -rf -- "$scratch"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP
git -C "$source_repo" archive "$revision" > "$scratch/source.tar"
mkdir -- "$output/source"
tar -xf "$scratch/source.tar" -C "$output/source"
git -C "$output/source" apply --check "$native_dir/libcoap-persistence.patch"
git -C "$output/source" apply "$native_dir/libcoap-persistence.patch"
cmake -S "$native_dir" -B "$output/build" \
  -DLIBCOAP_SOURCE="$output/source" -DCMAKE_BUILD_TYPE=Debug
cmake --build "$output/build" --parallel 2
ctest --test-dir "$output/build" --output-on-failure
printf 'OSCORE dependency and boundary checks built in %s\n' "$output/build"
