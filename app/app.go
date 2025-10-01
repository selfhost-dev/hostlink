package app

import (
	agentService "hostlink/app/service/agent"
	"hostlink/db/schema/taskschema"
	"hostlink/domain/agent"
	"hostlink/domain/nonce"
	gormRepo "hostlink/internal/repository/gorm"

	"gorm.io/gorm"
)

type Container struct {
	DB                  *gorm.DB
	RegistrationService *agentService.RegistrationService
}

func NewContainer(db *gorm.DB) *Container {
	// Initialize repositories
	agentRepo := gormRepo.NewAgentRepository(db)

	// Initialize services
	registrationSvc := agentService.NewRegistrationService(agentRepo)

	return &Container{
		DB:                  db,
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
	); err != nil {
		return err
	}

	// Migrate task schema (still needed for tasks)
	if err := c.DB.AutoMigrate(
		&taskschema.Task{},
	); err != nil {
		return err
	}

	return nil
}
