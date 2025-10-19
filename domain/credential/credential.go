// Package credential contains the domain for the credentail
package credential

import "time"

type Credential struct {
	ID        string     `json:"id"`
	Dialect   string     `json:"dialect"`
	Host      string     `json:"host"`
	Port      int        `json:"port"`
	Username  string     `json:"username"`
	PasswdEnc string     `json:"passwd_enc"`
	Password  *string    `json:"password"`
	AgentID   string     `json:"agent_id"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

type CredentialFilters struct {
	AgentID *string
}
