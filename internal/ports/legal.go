package ports

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Publish appends an immutable version and changes the global consent version
// in the same transaction. Ordinary settings writers cannot change that version.
type LegalRepo interface {
	Publish(context.Context, domain.LegalDraft) (domain.LegalPublication, error)
	Latest(context.Context, string, string) (domain.LegalDocument, error)
	Public(context.Context, string, string) (domain.LegalPublicDocument, error)
	History(context.Context, string, string, int64, int) ([]domain.LegalDocument, error)
	Accept(context.Context, int64, int64) error
	Status(context.Context, int64) (domain.LegalConsentStatus, error)
	AffectedUsers(context.Context) (int64, error)
	PurgeOrphans(context.Context) (int64, error)
}

// RegisteredUserWriter checks the durable legal version and stores consent in
// the same transaction as the account/credential write. Provisioning happens
// after this transaction. Admin and SSO creation use UserRepo.Create instead.
type RegisteredUserWriter interface {
	CreateRegistered(context.Context, *domain.User, int64) error
	ResumeRegistration(context.Context, int64, string, int64) error
}
