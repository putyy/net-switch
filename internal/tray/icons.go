package tray

import (
	_ "embed"
	"runtime"

	"fyne.io/systray"
)

//go:embed icons/tray-template.png
var trayTemplateIcon []byte

//go:embed icons/tray-color.png
var trayColorPNG []byte

//go:embed icons/tray-color.ico
var trayColorICO []byte

func setTrayIcon() {
	regularIcon := trayColorPNG
	if runtime.GOOS == "windows" {
		regularIcon = trayColorICO
	}
	systray.SetTemplateIcon(trayTemplateIcon, regularIcon)
}
