//go:build linux

package browser

import (
	"fmt"
	"net/url"
	"os/exec"
)

func Open(rawURL string) error {
	if err := validateLocalURL(rawURL); err != nil {
		return err
	}
	if err := exec.Command("xdg-open", rawURL).Start(); err != nil {
		return fmt.Errorf("open default browser: %w", err)
	}
	return nil
}

func validateLocalURL(rawURL string) error {
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return fmt.Errorf("invalid dashboard URL: %w", err)
	}
	if parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" {
		return fmt.Errorf("refusing to open a non-local dashboard URL")
	}
	return nil
}
