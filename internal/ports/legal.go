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
	History(context.Context, string, string, int64, int) ([]domain.LegalDocument, error)
}
