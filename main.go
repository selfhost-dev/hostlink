package main

import (
	"fmt"
	"hostlink/app/jobs/registrationjob"
	"hostlink/app/jobs/taskjob"
	"hostlink/config"
	"hostlink/config/appconf"
	"hostlink/db/schema/agentschema"
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
	if err := dbconn.Migrate(&agentschema.Agent{}); err != nil {
		log.Fatal("agent migration failed", err)
	}
	if err := dbconn.Migrate(&agentschema.AgentTag{}); err != nil {
		log.Fatal("agent tag migration failed", err)
	}
	if err := dbconn.Migrate(&agentschema.AgentRegistration{}); err != nil {
		log.Fatal("agent registration migration failed", err)
	}

	e := echo.New()

	// Add middleware for logging and recovery
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	config.AddRoutes(e)
	taskjob.Register()
	registrationJob := registrationjob.New()
	registrationJob.Register()

	log.Fatal(e.Start(fmt.Sprintf(":%s", appconf.Port())))
}
