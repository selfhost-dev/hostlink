package main

import (
	"context"
	"fmt"
	"hostlink/app"
	"hostlink/app/jobs/heartbeatjob"
	"hostlink/app/jobs/metricsjob"
	"hostlink/app/jobs/registrationjob"
	"hostlink/app/jobs/selfupdatejob"
	"hostlink/app/jobs/taskjob"
	"hostlink/app/services/agentstate"
	"hostlink/app/services/heartbeat"
	"hostlink/app/services/metrics"
	"hostlink/app/services/requestsigner"
	"hostlink/app/services/taskfetcher"
	"hostlink/app/services/taskreporter"
	"hostlink/app/services/updatecheck"
	"hostlink/app/services/updatedownload"
	"hostlink/app/services/updatepreflight"
	"hostlink/cmd/upgrade"
	"hostlink/config"
	"hostlink/config/appconf"
	"hostlink/internal/dbconn"
	"hostlink/internal/httpclient"
	"hostlink/internal/update"
	"hostlink/internal/validator"
	"hostlink/version"
	"log"
	"os"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/urfave/cli/v3"
)

func init() {
	_ = godotenv.Load()
}

func newApp() *cli.Command {
	return &cli.Command{
		Name:    "hostlink",
		Usage:   "Hostlink agent",
		Version: version.Version,
		Action:  runServer,
		Commands: []*cli.Command{
			{
				Name:  "version",
				Usage: "Print the version",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					fmt.Println(version.Version)
					return nil
				},
			},
			{
				Name:  "upgrade",
				Usage: "Upgrade the hostlink binary in-place",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "install-path",
						Usage:   "Target path to install the binary",
						Value:   "/usr/bin/hostlink",
						Sources: cli.EnvVars("HOSTLINK_INSTALL_PATH"),
					},
					&cli.BoolFlag{
						Name:  "dry-run",
						Usage: "Validate preconditions without performing the upgrade",
					},
					&cli.StringFlag{
						Name:   "base-dir",
						Usage:  "Override update base directory (for testing)",
						Hidden: true,
					},
					&cli.StringFlag{
						Name:   "update-id",
						Usage:  "Unique ID for this update operation",
						Hidden: true,
					},
					&cli.StringFlag{
						Name:   "source-version",
						Usage:  "Version being upgraded from",
						Hidden: true,
					},
				},
				Action: runUpgrade,
			},
		},
	}
}

const upgradeTimeout = 90 * time.Second

func runUpgrade(ctx context.Context, cmd *cli.Command) error {
	installPath := cmd.String("install-path")
	if installPath == "" {
		return fmt.Errorf("--install-path cannot be empty")
	}

	dryRun := cmd.Bool("dry-run")
	baseDir := cmd.String("base-dir")
	updateID := cmd.String("update-id")
	sourceVersion := cmd.String("source-version")

	// Resolve self path (the staged binary that was executed)
	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine self path: %w", err)
	}

	// Set up paths (use custom base-dir if provided, otherwise defaults)
	var paths update.Paths
	if baseDir != "" {
		paths = update.NewPaths(baseDir)
	} else {
		paths = update.DefaultPaths()
	}

	// Set up logger
	logger, cleanup, err := upgrade.NewLogger(upgrade.DefaultLogPath)
	if err != nil {
		// Fall back to stderr-only logging if we can't write the log file
		fmt.Fprintf(os.Stderr, "warning: cannot open log file: %v\n", err)
		logger = nil // Upgrader will use discard logger
	} else {
		defer cleanup()
	}

	// Build config
	cfg := &upgrade.Config{
		InstallPath:   installPath,
		SelfPath:      selfPath,
		BackupDir:     paths.BackupDir,
		LockPath:      paths.LockFile,
		StatePath:     paths.StateFile,
		HealthURL:     "http://127.0.0.1:" + appconf.Port() + "/health",
		TargetVersion: version.Version,
		UpdateID:      updateID,
		SourceVersion: sourceVersion,
		Logger:        logger,
	}

	u, err := upgrade.NewUpgrader(cfg)
	if err != nil {
		return err
	}

	if dryRun {
		results := u.DryRun(ctx)
		allPassed := true
		for _, r := range results {
			status := "PASS"
			if !r.Passed {
				status = "FAIL"
				allPassed = false
			}
			fmt.Fprintf(os.Stderr, "[%s] %s: %s\n", status, r.Name, r.Detail)
		}
		if !allPassed {
			return fmt.Errorf("dry-run: one or more checks failed")
		}
		return nil
	}

	// Set up timeout and signal handling
	ctx, cancel := context.WithTimeout(ctx, upgradeTimeout)
	defer cancel()

	stop := upgrade.WatchSignals(cancel)
	defer stop()

	return u.Run(ctx)
}

func runServer(ctx context.Context, cmd *cli.Command) error {
	db, err := dbconn.GetConn(
		dbconn.WithURL(appconf.DBURL()),
	)
	if err != nil {
		log.Fatal("db connection failed", err)
	}

	defer dbconn.Close()

	container := app.NewContainer(db)

	if err := container.Migrate(); err != nil {
		log.Fatal("migration failed", err)
	}

	e := echo.New()
	e.Validator = validator.New()

	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	config.AddRoutesV2(e, container)

	// Agent-related jobs run in goroutine after registration
	go func() {
		ctx := context.Background()
		registeredChan := make(chan bool, 1)

		registrationJob := registrationjob.New()
		registrationJob.Register(registeredChan)

		// Wait for registration to complete
		<-registeredChan
		log.Println("Agent registered, starting task job...")

		fetcher, err := taskfetcher.NewDefault()
		if err != nil {
			log.Printf("failed to initialize task fetcher: %v", err)
			return
		}
		reporter, err := taskreporter.NewDefault()
		if err != nil {
			log.Printf("failed to initialize task reporter: %v", err)
			return
		}
		taskJob := taskjob.New()
		taskJob.Register(ctx, fetcher, reporter)

		metricsReporter, err := metrics.New()
		if err != nil {
			log.Printf("failed to initialize metrics reporter: %v", err)
			return
		}
		metricsJob := metricsjob.New()
		metricsJob.Register(ctx, metricsReporter, metricsReporter)

		heartbeatSvc, err := heartbeat.New()
		if err != nil {
			log.Printf("failed to initialize heartbeat service: %v", err)
			return
		}
		heartbeatJob := heartbeatjob.New()
		heartbeatJob.Register(ctx, heartbeatSvc)

		// Self-update job (gated by config)
		if appconf.SelfUpdateEnabled() {
			startSelfUpdateJob(ctx)
		}
	}()

	return e.Start(fmt.Sprintf(":%s", appconf.Port()))
}

func startSelfUpdateJob(ctx context.Context) {
	paths := update.DefaultPaths()

	// Ensure update directories exist with correct permissions
	if err := update.InitDirectories(paths.BaseDir); err != nil {
		log.Printf("failed to initialize update directories: %v", err)
		return
	}

	// Clean staging dir on boot and ensure it's ready for use
	stagingMgr := updatedownload.NewStagingManager(paths.StagingDir, nil)
	if err := stagingMgr.Cleanup(); err != nil {
		log.Printf("failed to clean staging dir on boot: %v", err)
	}
	if err := stagingMgr.Prepare(); err != nil {
		log.Printf("failed to prepare staging dir: %v", err)
		return
	}

	// Load agent state for ID and signer
	state := agentstate.New(appconf.AgentStatePath())
	if err := state.Load(); err != nil {
		log.Printf("failed to load agent state for self-update: %v", err)
		return
	}
	agentID := state.GetAgentID()
	if agentID == "" {
		log.Printf("self-update: agent ID not available, skipping")
		return
	}

	// Create request signer
	signer, err := requestsigner.New(appconf.AgentPrivateKeyPath(), agentID)
	if err != nil {
		log.Printf("failed to create request signer for self-update: %v", err)
		return
	}

	// Create update checker
	checker, err := updatecheck.New(
		httpclient.NewClient(30*time.Second),
		appconf.ControlPlaneURL(),
		agentID,
		signer,
	)
	if err != nil {
		log.Printf("failed to create update checker: %v", err)
		return
	}

	// Create downloader
	downloader := updatedownload.NewDownloader(updatedownload.DefaultDownloadConfig())

	// Create preflight checker
	preflight := updatepreflight.New(updatepreflight.PreflightConfig{
		AgentBinaryPath: appconf.InstallPath(),
		UpdatesDir:      paths.BaseDir,
		StatFunc: func(path string) (uint64, error) {
			var stat syscall.Statfs_t
			if err := syscall.Statfs(path, &stat); err != nil {
				return 0, err
			}
			return stat.Bavail * uint64(stat.Bsize), nil
		},
	})

	// Create lock manager
	lockMgr := update.NewLockManager(update.LockConfig{
		LockPath: paths.LockFile,
	})

	// Create state writer
	stateWriter := update.NewStateWriter(update.StateConfig{
		StatePath: paths.StateFile,
	})

	// Configure trigger with update check interval
	triggerCfg := selfupdatejob.TriggerConfig{
		Interval: appconf.UpdateCheckInterval(),
	}

	job := selfupdatejob.NewWithConfig(selfupdatejob.SelfUpdateJobConfig{
		Trigger: func(ctx context.Context, fn func() error) {
			selfupdatejob.TriggerWithConfig(ctx, fn, triggerCfg)
		},
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lockMgr,
		StateWriter:      stateWriter,
		Spawn:            update.SpawnUpgrade,
		InstallBinary:    update.InstallBinary,
		CurrentVersion:   version.Version,
		InstallPath:      appconf.InstallPath(),
		StagingDir:       paths.StagingDir,
	})

	job.Register(ctx)
	log.Printf("self-update job started (interval: %s)", appconf.UpdateCheckInterval())
}

func main() {
	app := newApp()
	if err := app.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
