package nodesync

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type quotaBatchUsers struct {
	ports.UserRepo
	calls int
	ids   []int64
	shape string
	err   error
}

func (r *quotaBatchUsers) GetByID(context.Context, int64) (*domain.User, error) {
	panic("quota account closure must use the existing bounded batch interface")
}

func (r *quotaBatchUsers) ListByIDs(_ context.Context, ids []int64) ([]*domain.User, error) {
	r.calls++
	r.ids = slices.Clone(ids)
	if r.err != nil {
		return nil, r.err
	}
	var users []*domain.User
	for i := len(ids) - 1; i >= 0; i-- {
		users = append(users, &domain.User{ID: ids[i], TrafficLimitBytes: ids[i] * 1024, IPLimit: int(ids[i] % 3)})
	}
	switch r.shape {
	case "missing":
		users = users[1:]
	case "nil":
		users = append(users, nil)
	case "duplicate":
		users = append(users, users[0])
	case "foreign":
		users = append(users, &domain.User{ID: 9999})
	}
	return users, nil
}

func TestQuotaAccountBatchPreservesExactCurrentRosterClosure(t *testing.T) {
	for _, shape := range []string{"complete", "empty", "missing", "nil", "duplicate", "foreign", "error"} {
		t.Run(shape, func(t *testing.T) {
			snapshot := &ports.NativeDesiredSnapshot{}
			if shape != "empty" {
				for i := int64(601); i > 0; i-- {
					snapshot.Clients = append(snapshot.Clients, ports.NativeDesiredClient{Client: &domain.PSPClient{ID: i, UserID: i, DesiredEnable: i%2 == 0}})
				}
				snapshot.Clients = append(snapshot.Clients, ports.NativeDesiredClient{}, ports.NativeDesiredClient{Client: &domain.PSPClient{ID: 602, UserID: 19}})
			}
			readFailure := errors.New("owned quota read failure")
			repo := &quotaBatchUsers{shape: shape}
			if shape == "error" {
				repo.err = readFailure
			}
			service := &Service{users: repo}
			users, err := service.loadQuotaUsers(t.Context(), snapshot)
			if shape == "empty" {
				if err != nil || len(users) != 0 || repo.calls != 0 {
					t.Fatal("empty roster performed a fleet account read")
				}
				return
			}
			if repo.calls != 1 || len(repo.ids) != 601 {
				t.Fatal("roster accounts were not deduplicated into one repository batch")
			}
			for i, id := range repo.ids {
				if id != int64(i+1) {
					t.Fatal("account closure changed identity or ordering")
				}
			}
			if shape != "complete" {
				if err == nil || users != nil {
					t.Fatal("invalid batch returned partial quota accounts")
				}
				if shape == "error" && !errors.Is(err, readFailure) {
					t.Fatal("batch read failure lost its cause")
				}
				if shape == "missing" && !errors.Is(err, domain.ErrNotFound) {
					t.Fatal("missing account became a zero quota")
				}
				return
			}
			if err != nil || len(users) != 601 {
				t.Fatal("complete batch lost current quota accounts")
			}
			for id, user := range users {
				if user.ID != id || user.TrafficLimitBytes != id*1024 || user.IPLimit != int(id%3) {
					t.Fatal("batch result was bound to the wrong account or lost current quota facts")
				}
			}
		})
	}
}
