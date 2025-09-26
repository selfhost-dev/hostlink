package config

import (
	"hostlink/app"
	"hostlink/app/controller/agent"
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
	// agent routes moved to AddRoutesV2
}

// AddRoutesV2 uses dependency injection pattern for new controllers
func AddRoutesV2(e *echo.Echo, container *app.Container) {
	// Call old routes for backward compatibility
	AddRoutes(e)

	// Initialize handlers with dependencies
	agentHandler := agent.NewHandler(container.RegistrationService)

	// Register routes using the new pattern
	agentHandler.RegisterRoutes(e.Group("/api/v1/agent"))
}
