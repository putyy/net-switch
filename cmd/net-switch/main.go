package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	netswitch "github.com/putyy/net-switch"
	"github.com/putyy/net-switch/internal/app"
	"github.com/putyy/net-switch/internal/applog"
)

var (
	version = ""
	commit  = "unknown"
	builtAt = "unknown"
)

func main() {
	os.Exit(run())
}

func run() int {
	showVersion := flag.Bool("version", false, "show version information")
	dryRun := flag.Bool("dry-run", false, "print network operations without changing system settings")
	loginStart := flag.Bool("login-start", false, "mark the process as started by a login item")
	flag.Parse()
	currentVersion := version
	if currentVersion == "" {
		currentVersion = netswitch.Version()
	}

	if *showVersion {
		fmt.Printf("Net Switch %s (commit: %s, built: %s)\n", currentVersion, commit, builtAt)
		return 0
	}

	logManager, logErr := applog.New()
	if logErr != nil {
		log.Printf("Could not initialize file logging; continuing with standard error: %v", logErr)
	} else {
		previousOutput := log.Writer()
		log.SetOutput(io.MultiWriter(previousOutput, logManager))
		defer func() {
			log.SetOutput(previousOutput)
			if closeErr := logManager.Close(); closeErr != nil {
				log.Printf("Could not close the log file: %v", closeErr)
			}
		}()
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client := app.New(app.Options{
		Version:    currentVersion,
		DryRun:     *dryRun,
		LoginStart: *loginStart,
		Logs:       logManager,
	})
	if err := client.Run(ctx); err != nil {
		log.Printf("Net Switch failed to start: %v", err)
		return 1
	}
	return 0
}
