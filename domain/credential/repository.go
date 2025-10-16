package credential

import "context"

type Repository interface {
	Create(ctx context.Context, task *Credential) error
	FindAll(ctx context.Context, filters CredentialFilters) ([]Credential, error)
	FindByID(ctx context.Context, id string) (*Credential, error)
	Update(ctx context.Context, task *Credential) error
}

type Encrypter func(in string) (out string)
