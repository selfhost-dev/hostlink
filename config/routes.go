package config

import (
	"hostlink/app/controller/health"
	"hostlink/app/controller/hostlinkcontroller"
	"hostlink/app/controller/static"
	"hostlink/app/controller/tasks"

	"github.com/labstack/echo/v4"
)

func AddRoutes(e *echo.Echo) {
	root := e.Group("")
	apiRoute := e.Group("/api")

	v1Route := apiRoute.Group("/v1")

	static.Register(root)
	health.Register(root)
	tasks.Register(v1Route.Group("/tasks"))
	hostlinkcontroller.Register(v1Route.Group("/hostlink"))
}
