package config

import (
	"hostlink/app"
	"hostlink/app/controller/agent"
	"hostlink/app/controller/health"
	"hostlink/app/controller/static"
	"hostlink/app/controller/tasks"
	"hostlink/app/middleware/agentauth"

	"github.com/labstack/echo/v4"
)

// AddRoutesV2 uses dependency injection pattern for new controllers
func AddRoutesV2(e *echo.Echo, container *app.Container) {
	root := e.Group("")
	static.Register(root)
	health.Register(root)
	// Initialize middleware
	authMiddleware := agentauth.New(container.AgentRepository)

	// Initialize handlers with dependencies
	agentHandler := agent.NewHandler(container.RegistrationService)
	tasksHandler := tasks.NewHandler(container.TaskRepository)

	// Register routes using the new pattern
	agentHandler.RegisterRoutes(e.Group("/api/v1/agent"))

	// TODO: Remove v2 routes once proper auth is in place
	tasksHandler.RegisterRoutes(e.Group("/api/v2/tasks"))

	// Register authenticated task routes
	tasksGroup := e.Group("/api/v1/tasks")
	tasksGroup.Use(authMiddleware)
	tasksHandler.RegisterRoutes(tasksGroup)
}
