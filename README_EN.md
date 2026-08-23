# Net Switch

[简体中文](README.md)

Net Switch is an open-source network profile switching client for people who regularly move between home, office, lab, and other networks. It reduces the repetitive work of changing IP addresses, gateways, and DNS settings by hand.

## Why Net Switch exists

Different Wi-Fi networks often require different settings. Home networks usually use DHCP, while an office or lab may require a static IP, a specific gateway, or custom DNS servers. Changing these settings manually is tedious, and forgetting to restore them can leave a computer connected to Wi-Fi but unable to reach the network.

Net Switch turns that routine into a set-it-once workflow. Save a rule for each familiar Wi-Fi network, and the appropriate configuration can be restored whenever you reconnect.

## What it can do for you

- Show the current Wi-Fi, IPv4 address, subnet mask, gateway, and DNS servers.
- Save a different network profile for each Wi-Fi name.
- Apply the matching profile when you connect to a familiar Wi-Fi network.
- Keep the current settings or restore DHCP when no profile matches.
- Pause automatic switching, apply a profile manually, or restore DHCP at any time.
- Provide quick actions from the system tray and optionally start when you sign in.
- Offer a Chinese or English dashboard, with Chinese as the default.
- Keep recent results and logs on your device to help diagnose network problems.

## Who it is for

- Developers moving between home, office, and customer sites.
- People connecting to servers, NAS devices, cameras, or lab equipment.
- Network administrators who frequently switch between DHCP and static IPv4 settings.
- Anyone who wants to avoid repeatedly editing network settings by hand.

Download published versions from [GitHub Releases](https://github.com/putyy/netswitch/releases).

## Privacy

Net Switch runs locally, requires no account, and does not depend on a cloud service. The dashboard, configuration, and logs stay on your computer.

The project is still evolving. Issues describing real network setups and problems are welcome.
