#!/bin/sh

set -eu

if [ "$#" -gt 1 ]; then
    echo "用法: $0 [arm64|amd64]" >&2
    exit 2
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
version=$(tr -d '\r\n' < "$project_root/VERSION")
architecture=${1:-$(go env GOARCH)}

if ! printf '%s\n' "$version" | grep -Eq '^[0-9]+[.][0-9]+[.][0-9]+$'; then
    echo "VERSION 必须是三段数字版本号，例如 0.1.0" >&2
    exit 2
fi

case "$architecture" in
    arm64|amd64) ;;
    *)
        echo "当前仅支持 arm64 或 amd64" >&2
        exit 2
        ;;
esac

dist_dir="$script_dir/dist"
dmg_path="$dist_dir/Net-Switch-$version-$architecture.dmg"
work_dir=$(mktemp -d)

cleanup() {
    rm -rf -- "$work_dir"
}
trap cleanup EXIT HUP INT TERM

app_output="$work_dir/app"
staging_dir="$work_dir/dmg"
mkdir -p "$dist_dir" "$app_output" "$staging_dir"

sh "$script_dir/package-macos.sh" "$app_output" "$architecture"
cp -R "$app_output/Net Switch.app" "$staging_dir/Net Switch.app"
ln -s /Applications "$staging_dir/Applications"

if [ -e "$dmg_path" ]; then
    rm -f -- "$dmg_path"
fi

/usr/bin/hdiutil create \
    -volname "Net Switch" \
    -srcfolder "$staging_dir" \
    -format UDZO \
    -ov \
    "$dmg_path"

echo "安装包已生成: $dmg_path"
