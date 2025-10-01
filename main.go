package main

import (
	"fmt"
	"hostlink/app"
	"hostlink/app/jobs/registrationjob"
	"hostlink/app/jobs/taskjob"
	"hostlink/config"
	"hostlink/config/appconf"
	"hostlink/internal/dbconn"
	"log"

	_ "github.com/joho/godotenv/autoload"
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

	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	config.AddRoutesV2(e, container)

	// TODO(iAziz786): check if we can move this cron in app
	taskjob.Register()
	registrationJob := registrationjob.New()
	registrationJob.Register()

	log.Fatal(e.Start(fmt.Sprintf(":%s", appconf.Port())))
}
