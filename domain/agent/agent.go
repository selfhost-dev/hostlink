package agent

import (
	"errors"
	"time"
)

var (
	ErrPublicKeyNotFound = errors.New("public key not found")
	ErrAgentNotFound     = errors.New("agent not found")
)

type Agent struct {
	ID            string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     *time.Time
	Fingerprint   string
	PublicKey     string
	PublicKeyType string
	Status        string
	LastSeen      time.Time

	// Hardware fingerprint components
	HardwareHash string
	MachineID    string
	Hostname     string
	IPAddress    string
	MACAddress   string

	// Registration metadata
	TokenID      string
	RegisteredAt time.Time

	// Relations
	Tags          []AgentTag
	Registrations []AgentRegistration
}

type AgentTag struct {
	ID        uint
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
	AgentID   string
	Key       string
	Value     string
}

type AgentRegistration struct {
	ID               string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DeletedAt        *time.Time
	AgentID          string
	Fingerprint      string
	Event            string
	Success          bool
	Response         string
	Error            string
	HardwareSnapshot string
	SimilarityScore  int
}

type AgentFilters struct {
	Status      *string
	Fingerprint *string
}
