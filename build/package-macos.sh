#!/bin/sh

set -eu

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
    echo "用法: $0 <输出目录> [arm64|amd64]" >&2
    exit 2
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
output_dir=$1
version=$(tr -d '\r\n' < "$project_root/VERSION")
architecture=${2:-$(go env GOARCH)}

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

mkdir -p "$output_dir"
output_dir=$(CDPATH= cd -- "$output_dir" && pwd)
if [ "$output_dir" = "/" ]; then
    echo "拒绝将系统根目录作为输出目录" >&2
    exit 2
fi

app_path="$output_dir/Net Switch.app"
contents_path="$app_path/Contents"
executable_path="$contents_path/MacOS/net-switch"

if [ -e "$app_path" ]; then
    rm -rf -- "$app_path"
fi
mkdir -p "$contents_path/MacOS" "$contents_path/Resources"
cp "$script_dir/macos/Info.plist" "$contents_path/Info.plist"
cp "$script_dir/icons/net-switch.icns" "$contents_path/Resources/net-switch.icns"

/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $version" "$contents_path/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $version" "$contents_path/Info.plist"

commit=$(git -C "$project_root" rev-parse --short HEAD 2>/dev/null || printf 'unknown')
built_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
linker_flags="-s -w -X main.version=$version -X main.commit=$commit -X main.builtAt=$built_at"

(
    cd "$project_root"
    CGO_ENABLED=1 GOOS=darwin GOARCH="$architecture" go build \
        -trimpath \
        -ldflags "$linker_flags" \
        -o "$executable_path" \
        ./cmd/net-switch
)

chmod 755 "$executable_path"
plutil -lint "$contents_path/Info.plist"

codesign_identity=${CODESIGN_IDENTITY:--}
if [ "$codesign_identity" != "none" ]; then
    if [ "$codesign_identity" = "-" ]; then
        /usr/bin/codesign --force --sign - "$app_path"
    else
        /usr/bin/codesign --force --options runtime --timestamp --sign "$codesign_identity" "$app_path"
    fi
    /usr/bin/codesign --verify --strict "$app_path"
fi

echo "已生成: $app_path"
