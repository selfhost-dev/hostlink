package main

import (
	"context"
	"fmt"
	"hostlink/app"
	"hostlink/app/jobs/metricsjob"
	"hostlink/app/jobs/registrationjob"
	"hostlink/app/jobs/taskjob"
	"hostlink/app/services/metrics"
	"hostlink/app/services/taskfetcher"
	"hostlink/app/services/taskreporter"
	"hostlink/config"
	"hostlink/config/appconf"
	"hostlink/internal/dbconn"
	"hostlink/internal/validator"
	"log"

	"github.com/joho/godotenv"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func main() {
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

	// TODO(iAziz786): check if we can move this cron in app
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
	}()

	log.Fatal(e.Start(fmt.Sprintf(":%s", appconf.Port())))
}

func init() {
	_ = godotenv.Load()
}
