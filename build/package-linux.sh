#!/bin/sh

set -eu

if [ "$#" -gt 1 ]; then
    echo "用法: $0 [amd64|arm64]" >&2
    exit 2
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

for command_name in go tar dpkg-deb; do
    if ! command -v "$command_name" >/dev/null 2>&1; then
        echo "未找到 $command_name" >&2
        exit 1
    fi
done

architecture=${1:-$(go env GOARCH)}
version=$(tr -d '\r\n' < "$project_root/VERSION")

if ! printf '%s\n' "$version" | grep -Eq '^[0-9]+[.][0-9]+[.][0-9]+$'; then
    echo "VERSION 必须是三段数字版本号，例如 0.1.0" >&2
    exit 2
fi
case "$architecture" in
    amd64|arm64) ;;
    *)
        echo "Linux 打包仅支持 amd64 或 arm64" >&2
        exit 2
        ;;
esac

dist_dir="$script_dir/dist"
archive_name="Net-Switch-$version-linux-$architecture"
archive_path="$dist_dir/$archive_name.tar.gz"
deb_path="$dist_dir/Net-Switch-$version-linux-$architecture.deb"
work_dir=$(mktemp -d)

cleanup() {
    rm -rf -- "$work_dir"
}
trap cleanup EXIT HUP INT TERM

commit=$(git -C "$project_root" rev-parse --short HEAD 2>/dev/null || printf 'unknown')
built_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
linker_flags="-s -w -X main.version=$version -X main.commit=$commit -X main.builtAt=$built_at"

archive_root="$work_dir/$archive_name"
deb_root="$work_dir/deb"
mkdir -p "$dist_dir" "$archive_root" "$deb_root/DEBIAN" "$deb_root/usr/bin" "$deb_root/usr/share/applications" "$deb_root/usr/share/icons/hicolor/512x512/apps"

(
    cd "$project_root"
    CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" go build \
        -trimpath \
        -ldflags "$linker_flags" \
        -o "$archive_root/net-switch" \
        ./cmd/net-switch
)

chmod 755 "$archive_root/net-switch"
cp "$script_dir/linux/net-switch.desktop" "$archive_root/net-switch.desktop"
cp "$script_dir/icons/net-switch.png" "$archive_root/net-switch.png"
cp "$script_dir/linux/README.txt" "$archive_root/README.txt"

cp "$archive_root/net-switch" "$deb_root/usr/bin/net-switch"
cp "$script_dir/linux/net-switch.desktop" "$deb_root/usr/share/applications/net-switch.desktop"
cp "$script_dir/icons/net-switch.png" "$deb_root/usr/share/icons/hicolor/512x512/apps/net-switch.png"
sed \
    -e "s/{{VERSION}}/$version/g" \
    -e "s/{{ARCHITECTURE}}/$architecture/g" \
    "$script_dir/linux/control" > "$deb_root/DEBIAN/control"
chmod 755 "$deb_root/usr/bin/net-switch"
chmod 644 "$deb_root/usr/share/applications/net-switch.desktop" "$deb_root/DEBIAN/control"
chmod 644 "$deb_root/usr/share/icons/hicolor/512x512/apps/net-switch.png"

rm -f -- "$archive_path" "$deb_path"
tar -C "$work_dir" -czf "$archive_path" "$archive_name"
dpkg-deb --root-owner-group --build "$deb_root" "$deb_path"

echo "Linux 便携包已生成: $archive_path"
echo "Linux DEB 已生成: $deb_path"
