package main

import (
	"fmt"
	"hostlink/app/jobs/taskjob"
	"hostlink/config"
	"hostlink/config/appconf"
	"hostlink/db/schema/taskschema"
	"hostlink/internal/dbconn"
	"log"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func main() {
	_, err := dbconn.GetConn(
		dbconn.WithURL(appconf.DBURL()),
	)
	if err != nil {
		log.Fatal("db connection failed", err)
	}

	defer dbconn.Close()

	if err := dbconn.Migrate(&taskschema.Task{}); err != nil {
		log.Fatal("migration failed", err)
	}

	e := echo.New()

	// Add middleware for logging and recovery
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	config.AddRoutes(e)
	taskjob.Register()

	log.Fatal(e.Start(fmt.Sprintf(":%s", appconf.Port())))
}
