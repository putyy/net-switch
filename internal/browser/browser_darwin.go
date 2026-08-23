//go:build darwin

package browser

import (
	"fmt"
	"net/url"
	"os/exec"
)

func Open(rawURL string) error {
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return fmt.Errorf("invalid dashboard URL: %w", err)
	}
	if parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" {
		return fmt.Errorf("refusing to open a non-local dashboard URL")
	}

	if err := exec.Command("/usr/bin/open", rawURL).Start(); err != nil {
		return fmt.Errorf("open default browser: %w", err)
	}
	return nil
}
