package config

import (
	"hostlink/app"
	"hostlink/app/controller/agent"
	"hostlink/app/controller/health"
	"hostlink/app/controller/hostlinkcontroller"
	"hostlink/app/controller/static"
	"hostlink/app/controller/tasks"
	"hostlink/app/middleware/agentauth"

	"github.com/labstack/echo/v4"
)

func AddRoutes(e *echo.Echo) {
	root := e.Group("")
	apiRoute := e.Group("/api")

	v1Route := apiRoute.Group("/v1")

	static.Register(root)
	health.Register(root)
	hostlinkcontroller.Register(v1Route.Group("/hostlink"))
	// agent routes and tasks routes moved to AddRoutesV2
}

// AddRoutesV2 uses dependency injection pattern for new controllers
func AddRoutesV2(e *echo.Echo, container *app.Container) {
	// Call old routes for backward compatibility
	AddRoutes(e)

	// Initialize middleware
	authMiddleware := agentauth.New(container.AgentRepository)

	// Initialize handlers with dependencies
	agentHandler := agent.NewHandler(container.RegistrationService)
	tasksHandler := tasks.NewHandler(container.TaskRepository)

	// Register routes using the new pattern
	agentHandler.RegisterRoutes(e.Group("/api/v1/agent"))

	// Register tasks routes with authentication
	tasksGroup := e.Group("/api/v1/tasks")
	tasksGroup.Use(authMiddleware)
	tasksHandler.RegisterRoutes(tasksGroup)
}
