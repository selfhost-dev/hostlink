// Package credential contains the domain for the credentail
package credential

import "time"

type Credential struct {
	ID            string  `json:"id"`
	Dialect       string  `json:"dialect"`
	Host          string  `json:"host"`
	Port          int     `json:"port"`
	Username      string  `json:"username"`
	Database      string  `json:"database"`
	PasswdEnc     string  `json:"passwd_enc"`
	Password      *string `json:"password"`
	SSLMode       string  `json:"ssl_mode,omitempty"`
	TLSEnabled    bool    `json:"tls_enabled,omitempty"`
	DataDirectory string  `json:"data_directory"`
	AgentID       string  `json:"agent_id"`
	// A Coolify database's entry says so (Source "coolify"), names its
	// container (Coolify names it after the database uuid) and, for
	// PostgreSQL, carries the agent's own monitoring login (selfhost_monitor),
	// so the agent never monitors as the customer, whose password can change.
	// Empty for every other credential.
	Source           string     `json:"source,omitempty"`
	ContainerName    string     `json:"container_name,omitempty"`
	MonitorUsername  string     `json:"monitor_username,omitempty"`
	MonitorPasswdEnc string     `json:"monitor_passwd_enc,omitempty"`
	MonitorPassword  *string    `json:"-"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	DeletedAt        *time.Time `json:"deleted_at,omitempty"`
}

// IsCoolify: a Coolify database, collected per container by the Docker path,
// never through the single-database path at localhost.
func (c Credential) IsCoolify() bool {
	return c.Source == "coolify" || c.ContainerName != ""
}

type CredentialFilters struct {
	AgentID *string
}
