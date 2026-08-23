#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
icons_dir="$script_dir/icons"
tray_icons_dir="$project_root/internal/tray/icons"

for command_name in magick go; do
    if ! command -v "$command_name" >/dev/null 2>&1; then
        echo "Missing required icon tool: $command_name" >&2
        exit 1
    fi
done

work_dir=$(mktemp -d)
cleanup() {
    rm -rf -- "$work_dir"
}
trap cleanup EXIT HUP INT TERM

iconset="$work_dir/NetSwitch.iconset"
mkdir -p "$icons_dir" "$tray_icons_dir" "$iconset"

render_png() {
    source_file=$1
    size=$2
    output_file=$3
    magick -background none "$source_file" -resize "${size}x${size}" -depth 8 "$output_file"
}

render_png "$project_root/web/logo.svg" 512 "$icons_dir/net-switch.png"
magick -background none "$project_root/web/logo.svg" \
    -define icon:auto-resize=256,128,64,48,32,16 \
    "$icons_dir/net-switch.ico"

render_png "$project_root/web/logo.svg" 64 "$tray_icons_dir/tray-color.png"
render_png "$icons_dir/tray-template.svg" 64 "$tray_icons_dir/tray-template.png"
magick -background none "$project_root/web/logo.svg" \
    -define icon:auto-resize=64,48,32,24,16 \
    "$tray_icons_dir/tray-color.ico"

render_png "$project_root/web/logo.svg" 16 "$iconset/icon_16x16.png"
render_png "$project_root/web/logo.svg" 32 "$iconset/icon_16x16@2x.png"
render_png "$project_root/web/logo.svg" 32 "$iconset/icon_32x32.png"
render_png "$project_root/web/logo.svg" 64 "$iconset/icon_32x32@2x.png"
render_png "$project_root/web/logo.svg" 128 "$iconset/icon_128x128.png"
render_png "$project_root/web/logo.svg" 256 "$iconset/icon_128x128@2x.png"
render_png "$project_root/web/logo.svg" 256 "$iconset/icon_256x256.png"
render_png "$project_root/web/logo.svg" 512 "$iconset/icon_256x256@2x.png"
render_png "$project_root/web/logo.svg" 512 "$iconset/icon_512x512.png"
render_png "$project_root/web/logo.svg" 1024 "$iconset/icon_512x512@2x.png"
(
    cd "$project_root"
    go run ./build/tools/icnspack \
        "$icons_dir/net-switch.icns" \
        "icp4=$iconset/icon_16x16.png" \
        "icp5=$iconset/icon_32x32.png" \
        "icp6=$iconset/icon_32x32@2x.png" \
        "ic07=$iconset/icon_128x128.png" \
        "ic08=$iconset/icon_256x256.png" \
        "ic09=$iconset/icon_512x512.png" \
        "ic10=$iconset/icon_512x512@2x.png"
)

echo "Icon assets generated in $icons_dir and $tray_icons_dir"
