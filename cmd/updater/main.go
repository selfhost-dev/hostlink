package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"hostlink/internal/update"
)

const (
	// Default paths
	DefaultBinaryPath  = "/usr/bin/hostlink"
	DefaultBaseDir     = "/var/lib/hostlink/updates"
	DefaultHealthURL   = "http://localhost:8080/health"
	DefaultServiceName = "hostlink"
)

func main() {
	// Parse command line flags
	var (
		binaryPath    = flag.String("binary", DefaultBinaryPath, "Path to the agent binary")
		baseDir       = flag.String("base-dir", DefaultBaseDir, "Base directory for update files")
		healthURL     = flag.String("health-url", DefaultHealthURL, "Health check URL")
		targetVersion = flag.String("version", "", "Target version to verify after update (required)")
		showVersion   = flag.Bool("v", false, "Print version and exit")
	)
	flag.Parse()

	if *showVersion {
		printVersion()
		os.Exit(0)
	}

	if *targetVersion == "" {
		// Try to read from state file
		paths := update.NewPaths(*baseDir)
		stateWriter := update.NewStateWriter(update.StateConfig{StatePath: paths.StateFile})
		state, err := stateWriter.Read()
		if err != nil || state.TargetVersion == "" {
			log.Fatal("target version is required: use -version flag or ensure state.json has target version")
		}
		*targetVersion = state.TargetVersion
	}

	// Build paths
	paths := update.NewPaths(*baseDir)

	// Create configuration
	cfg := &UpdaterConfig{
		AgentBinaryPath:     *binaryPath,
		BackupDir:           paths.BackupDir,
		StagingDir:          paths.StagingDir,
		LockPath:            paths.LockFile,
		StatePath:           paths.StateFile,
		HealthURL:           *healthURL,
		TargetVersion:       *targetVersion,
		ServiceStopTimeout:  30 * time.Second,
		ServiceStartTimeout: 30 * time.Second,
		HealthCheckRetries:  5,
		HealthCheckInterval: 5 * time.Second,
		HealthInitialWait:   5 * time.Second,
		LockRetries:         5,
		LockRetryInterval:   1 * time.Second,
	}

	// Create updater
	updater := NewUpdater(cfg)
	updater.onPhaseChange = func(phase Phase) {
		log.Printf("Phase: %s", phase)
	}

	// Create context with overall timeout (90s budget)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Set up signal handler: SIGTERM/SIGINT simply cancel the context.
	// Run() checks ctx.Err() between phases and does its own cleanup.
	stopSignals := WatchSignals(cancel)
	defer stopSignals()

	// Run the update - Run() owns all cleanup/rollback.
	if err := updater.Run(ctx); err != nil {
		log.Printf("Update failed: %v", err)
		os.Exit(1)
	}

	log.Println("Update completed successfully")
}

// Version information (set via ldflags)
var (
	version = "dev"
	commit  = "unknown"
)

func printVersion() {
	fmt.Printf("hostlink-updater %s (%s)\n", version, commit)
}
