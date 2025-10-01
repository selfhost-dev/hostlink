package app

import (
	agentService "hostlink/app/service/agent"
	"hostlink/domain/agent"
	"hostlink/domain/nonce"
	"hostlink/domain/task"
	gormRepo "hostlink/internal/repository/gorm"

	"gorm.io/gorm"
)

type Container struct {
	DB                  *gorm.DB
	AgentRepository     agent.Repository
	TaskRepository      task.Repository
	RegistrationService *agentService.RegistrationService
}

func NewContainer(db *gorm.DB) *Container {
	// Initialize repositories
	agentRepo := gormRepo.NewAgentRepository(db)
	taskRepo := gormRepo.NewTaskRepository(db)

	// Initialize services
	registrationSvc := agentService.NewRegistrationService(agentRepo)

	return &Container{
		DB:                  db,
		AgentRepository:     agentRepo,
		TaskRepository:      taskRepo,
		RegistrationService: registrationSvc,
	}
}

func (c *Container) Migrate() error {
	// Migrate domain models
	if err := c.DB.AutoMigrate(
		&agent.Agent{},
		&agent.AgentTag{},
		&agent.AgentRegistration{},
		&nonce.Nonce{},
		&task.Task{},
	); err != nil {
		return err
	}

	return nil
}
