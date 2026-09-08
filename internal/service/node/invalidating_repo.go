package node

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Subscription-cache invalidation used to be a call each mutating method had
// to remember, and 15 of the 17 forgot — including SetEnabled, so a node the
// operator took out of rotation was still handed to clients for up to a
// minute, and DeleteSeparator, so a disabled separator kept rendering while
// the admin UI reported success.
//
// Remembering is the wrong mechanism: fixing the 15 leaves the trap set for
// the 16th. These decorators make the invalidation a property of the WRITE, so
// a new mutating method gets it without knowing this file exists.
//
// Scoped deliberately to the repo handles node.Service holds. The traffic poll
// and the health loop keep undecorated handles, which is what makes this safe:
// they write their own columns (counters, health) several times a minute, and
// invalidating on those would keep the render cache permanently empty. That
// operator-owned / poll-owned split is not invented here — it is the same one
// the "column-scoped write" comments throughout this package already maintain,
// and it is why only the operator-owned writers are wrapped below.
//
// Which writers count as subscription-visible:
//
//	Create / Update / Delete            a node appears or disappears
//	UpdateMetadata                      display name, address, region, tags
//	UpdateEnabled                       the node enters or leaves the output
//	UpdateInboundConfig                 protocol, port, TLS, stream settings
//	BatchUpdateSortOrder                the order clients see
//
// And which deliberately do NOT, because the poll owns them and render never
// reads them: UpdateTrafficCounters, BatchUpdateTrafficCounters, UpdateHealth,
// UpdateCertBinding. TestInvalidatingNodeRepoCoversEveryWriter pins both lists
// so a writer added to ports.NodeRepo forces a decision rather than silently
// defaulting to "no invalidation".
type invalidatingNodeRepo struct {
	ports.NodeRepo
	notify func()
}

// after fires the invalidation only on a successful write. A failed write
// changed nothing, and dropping every user's rendered subscription to re-render
// identical output is a stampede for no reason.
func after(err error, notify func()) error {
	if err == nil && notify != nil {
		notify()
	}
	return err
}

func (r invalidatingNodeRepo) Create(ctx context.Context, n *domain.Node) error {
	return after(r.NodeRepo.Create(ctx, n), r.notify)
}

func (r invalidatingNodeRepo) Update(ctx context.Context, n *domain.Node) error {
	return after(r.NodeRepo.Update(ctx, n), r.notify)
}

func (r invalidatingNodeRepo) UpdateMetadata(ctx context.Context, n *domain.Node) error {
	return after(r.NodeRepo.UpdateMetadata(ctx, n), r.notify)
}

func (r invalidatingNodeRepo) UpdateInboundConfig(ctx context.Context, n *domain.Node) error {
	return after(r.NodeRepo.UpdateInboundConfig(ctx, n), r.notify)
}

func (r invalidatingNodeRepo) UpdateEnabled(ctx context.Context, id int64, enabled bool) error {
	return after(r.NodeRepo.UpdateEnabled(ctx, id, enabled), r.notify)
}

func (r invalidatingNodeRepo) BatchUpdateSortOrder(ctx context.Context, updates []ports.NodeSortUpdate) error {
	return after(r.NodeRepo.BatchUpdateSortOrder(ctx, updates), r.notify)
}

func (r invalidatingNodeRepo) Delete(ctx context.Context, id int64) error {
	return after(r.NodeRepo.Delete(ctx, id), r.notify)
}

// invalidatingSeparatorRepo is the same idea for the layout rows. Every writer
// here is subscription-visible — a separator has no poll-owned columns at all,
// which is why the whole write surface is wrapped and there is no exemption
// list to keep.
type invalidatingSeparatorRepo struct {
	ports.SeparatorRepo
	notify func()
}

func (r invalidatingSeparatorRepo) Create(ctx context.Context, e *domain.SeparatorEntry) error {
	return after(r.SeparatorRepo.Create(ctx, e), r.notify)
}

func (r invalidatingSeparatorRepo) Update(ctx context.Context, e *domain.SeparatorEntry) error {
	return after(r.SeparatorRepo.Update(ctx, e), r.notify)
}

func (r invalidatingSeparatorRepo) Delete(ctx context.Context, id int64) error {
	return after(r.SeparatorRepo.Delete(ctx, id), r.notify)
}

func (r invalidatingSeparatorRepo) BatchUpdateSortOrder(ctx context.Context, updates []ports.SeparatorSortUpdate) error {
	return after(r.SeparatorRepo.BatchUpdateSortOrder(ctx, updates), r.notify)
}
