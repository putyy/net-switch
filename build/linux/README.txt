Net Switch for Linux

Requirements:
- A graphical desktop session with a system tray
- NetworkManager and nmcli
- A desktop polkit authentication agent
- xdg-open

Run ./net-switch. The management interface opens from the system tray.
NetworkManager may request system authorization when a rule changes IPv4 or
DNS settings.
