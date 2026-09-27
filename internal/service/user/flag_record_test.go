package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

// The two ways the automatic location suspension ends outside the traffic
// poll, recorded in the flag history (flag_records, source geo_auto): a staff
// resume, and a suspension of another reason written over it. The suspension
// and its expiry lift are the poll's and are recorded there.

// flagAppend is one Append call as the flag history received it, with the
// context's state: a database refuses a cancelled context, and an unbounded
// write can hang a request.
type flagAppend struct {
	recs     []domain.FlagRecord
	ctxErr   error
	deadline bool
}

// fakeFlagHistory keeps every Append call. A cancelled context fails the call
// after it is kept, as the database does; err, when set, fails every call.
type fakeFlagHistory struct {
	mu    sync.Mutex
	calls []flagAppend
	err   error
}

// String names each record by account and event, for failure messages.
func (a flagAppend) String() string {
	parts := make([]string, 0, len(a.recs))
	for _, r := range a.recs {
		parts = append(parts, fmt.Sprintf("%d:%s/%s", r.UserID, r.Source, r.Event))
	}
	return fmt.Sprintf("{[%s] ctxErr=%v deadline=%v}", strings.Join(parts, " "), a.ctxErr, a.deadline)
}

func (f *fakeFlagHistory) Append(ctx context.Context, recs []domain.FlagRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, bounded := ctx.Deadline()
	f.calls = append(f.calls, flagAppend{recs: slices.Clone(recs), ctxErr: ctx.Err(), deadline: bounded})
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.err
}

// records is every record appended on a live context: what the table holds.
func (f *fakeFlagHistory) records() []domain.FlagRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.FlagRecord
	for _, c := range f.calls {
		if c.ctxErr == nil {
			out = append(out, c.recs...)
		}
	}
	return out
}

// requireGeoAutoRecord checks one record's fixed shape: user, geo_auto
// source, the event, suspended → nothing, the code, no state, stamped within
// [before, after].
func requireGeoAutoRecord(t *testing.T, r domain.FlagRecord, uid int64, ev domain.FlagEvent, code string, before, after time.Time) {
	t.Helper()
	if r.UserID != uid || r.Source != domain.FlagSourceGeoAuto || r.Event != ev ||
		r.Level != domain.FlagLevelNone || r.PrevLevel != domain.FlagLevelSuspended || r.State != "" || r.Code != code {
		t.Fatalf("record = %+v, want user %d geo_auto %s suspended→none, code %q", r, uid, ev, code)
	}
	if r.AtMS < before.UnixMilli() || r.AtMS > after.UnixMilli() {
		t.Fatalf("record stamped %d, want within [%d, %d]", r.AtMS, before.UnixMilli(), after.UnixMilli())
	}
}

// A staff resume of the detector's own suspension is recorded as geo_auto
// ending, coded admin_resume: the one lift the history cannot learn from the
// clock. It carries no params; who resumed is the audit log's to say.
func TestResumeServiceAndSync_RecordsAStaffLiftOfGeoAuto(t *testing.T) {
	at := time.Now().Add(-10 * time.Minute)
	h := newGeoAutoHarness(&domain.User{ID: 7, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: &at})
	flags := &fakeFlagHistory{}
	h.svc.SetFlagRecorder(flags)

	before := time.Now()
	if err := h.svc.ResumeServiceAndSync(context.Background(), 7); err != nil {
		t.Fatalf("resume: %v", err)
	}
	after := time.Now()

	recs := flags.records()
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want one", recs)
	}
	requireGeoAutoRecord(t, recs[0], 7, domain.FlagAutoLiftedAdmin, "admin_resume", before, after)
	if recs[0].Params != nil {
		t.Fatalf("params = %s, want none", recs[0].Params)
	}
	if c := flags.calls[0]; c.ctxErr != nil || !c.deadline {
		t.Fatalf("appended on a context with err %v, deadline %v; want a live context with its own bound", c.ctxErr, c.deadline)
	}
}

// Resuming any other reason — a person's pause, the blocked-client hold, a
// human geo suspension — or an account with no hold at all ends nothing the
// geo_auto history tracks, and records nothing.
func TestResumeServiceAndSync_RecordsNothingForOtherReasons(t *testing.T) {
	at := time.Now().Add(-10 * time.Minute)
	h := newGeoAutoHarness(
		&domain.User{ID: 7, Enabled: true, ServiceDisabledReason: domain.DisabledServiceManual, ServiceDisabledAt: &at},
		&domain.User{ID: 8, Enabled: true, ServiceDisabledReason: domain.DisabledBlockedClient, ServiceDisabledAt: &at},
		&domain.User{ID: 9, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAnomaly, ServiceDisabledAt: &at},
		&domain.User{ID: 10, Enabled: true},
	)
	flags := &fakeFlagHistory{}
	h.svc.SetFlagRecorder(flags)

	for _, uid := range []int64{7, 8, 9, 10} {
		if err := h.svc.ResumeServiceAndSync(context.Background(), uid); err != nil {
			t.Fatalf("resume %d: %v", uid, err)
		}
	}
	if len(flags.calls) != 0 {
		t.Fatalf("appends = %+v, want none", flags.calls)
	}
}

// The resume has committed once its write lands, so a request cancelled
// right after it (the admin closed the tab) still records it: on a context
// detached from the request's cancellation, bounded on its own.
func TestResumeServiceAndSync_RecordsOnADetachedContext(t *testing.T) {
	at := time.Now().Add(-10 * time.Minute)
	h := newGeoAutoHarness(&domain.User{ID: 7, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: &at})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.svc.users = &cancelAfterServiceWrite{memoryUserRepo: h.repo, cancel: cancel}
	flags := &fakeFlagHistory{}
	h.svc.SetFlagRecorder(flags)

	_ = h.svc.ResumeServiceAndSync(ctx, 7)

	if ctx.Err() == nil || h.repo.byID[7].ServiceDisabledReason != domain.DisabledNone {
		t.Fatal("precondition: want the resume written and the request cancelled after")
	}
	if recs := flags.records(); len(recs) != 1 || recs[0].Event != domain.FlagAutoLiftedAdmin {
		t.Fatalf("records = %+v (appends %+v), want the committed resume recorded", recs, flags.calls)
	}
}

// cancelAfterServiceWrite cancels the caller's context once a service-state
// write has committed: a request that goes away mid-transition.
type cancelAfterServiceWrite struct {
	*memoryUserRepo
	cancel func()
}

func (r *cancelAfterServiceWrite) UpdateServiceState(ctx context.Context, userID int64, reason domain.AutoDisabledReason, detail string, disabledAt *time.Time) error {
	err := r.memoryUserRepo.UpdateServiceState(ctx, userID, reason, detail, disabledAt)
	r.cancel()
	return err
}

// The record is a by-product of the resume, which a person asked for and
// which has already happened: a refused append fails nothing, and is
// counted so a history that stopped growing shows somewhere besides a Warn.
func TestResumeServiceAndSync_FlagRecorderFailureDoesNotFailTheResume(t *testing.T) {
	metrics.Reset()
	at := time.Now().Add(-10 * time.Minute)
	h := newGeoAutoHarness(&domain.User{ID: 7, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: &at})
	h.svc.SetFlagRecorder(&fakeFlagHistory{err: errors.New("database is locked")})

	if err := h.svc.ResumeServiceAndSync(context.Background(), 7); err != nil {
		t.Fatalf("resume = %v, want success with the record refused", err)
	}
	if h.repo.byID[7].ServiceDisabledReason != domain.DisabledNone {
		t.Fatal("the resume was not written")
	}
	var got int64
	for _, c := range metrics.Take().Counters {
		if c.Name == "psp_flag_record_write_errors_total" {
			got = c.Value
		}
	}
	if got != 1 {
		t.Fatalf("psp_flag_record_write_errors_total = %d, want 1", got)
	}
}

// Another suspension written over geo_auto — a person's pause, the
// blocked-client policy, a human geo suspension — ends the automatic one as
// surely as a resume, and hands the account to a hold that no clock lifts.
// Recorded as auto_replaced, with the reason that replaced it. Suspending an
// account that held nothing ends no geo_auto and records nothing; nor does a
// quota suspension, which a hard hold refuses outright.
func TestSetServiceSuspendedAndSync_RecordsTheReplacementOfGeoAuto(t *testing.T) {
	at := time.Now().Add(-10 * time.Minute)
	h := newGeoAutoHarness(
		&domain.User{ID: 7, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: &at},
		&domain.User{ID: 8, Enabled: true},
		&domain.User{ID: 9, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: &at},
	)
	flags := &fakeFlagHistory{}
	h.svc.SetFlagRecorder(flags)

	before := time.Now()
	if err := h.svc.SetServiceSuspendedAndSync(context.Background(), 7, domain.DisabledServiceManual, "paused by staff"); err != nil {
		t.Fatalf("replace geo_auto: %v", err)
	}
	after := time.Now()
	if err := h.svc.SetServiceSuspendedAndSync(context.Background(), 8, domain.DisabledServiceManual, "paused by staff"); err != nil {
		t.Fatalf("suspend a clear account: %v", err)
	}
	if err := h.svc.SetServiceSuspendedAndSync(context.Background(), 9, domain.DisabledTrafficExceeded, "over quota"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("quota over geo_auto = %v, want ErrConflict", err)
	}

	recs := flags.records()
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want only user 7's replacement", recs)
	}
	requireGeoAutoRecord(t, recs[0], 7, domain.FlagAutoReplaced, "replaced", before, after)
	var params map[string]any
	if err := json.Unmarshal(recs[0].Params, &params); err != nil {
		t.Fatalf("params %s: %v", recs[0].Params, err)
	}
	if want := map[string]any{"replaced_by": string(domain.DisabledServiceManual)}; !reflect.DeepEqual(params, want) {
		t.Fatalf("params = %v, want %v", params, want)
	}
	if c := flags.calls[0]; c.ctxErr != nil || !c.deadline {
		t.Fatalf("appended on a context with err %v, deadline %v; want a live context with its own bound", c.ctxErr, c.deadline)
	}
}

// Guard: the user service is handed a flag history it can only APPEND to,
// for the reasons the traffic poll is (traffic.TestFlagRecorderIsAppendOnly):
// a history anyone can rewrite is not one, and no service decision may read
// it back. Mutation: add a method to FlagRecorder (and to the fake above).
func TestFlagRecorderIsAppendOnly(t *testing.T) {
	var got []string
	for m := range reflect.TypeFor[FlagRecorder]().Methods() {
		got = append(got, m.Name)
	}
	if want := []string{"Append"}; !slices.Equal(got, want) {
		t.Fatalf("FlagRecorder exposes %v, want exactly %v", got, want)
	}
	f, ok := reflect.TypeFor[Service]().FieldByName("flagRec")
	if !ok {
		t.Fatal("Service has no flagRec field; update this test with the recorder's new home")
	}
	if f.Type != reflect.TypeFor[FlagRecorder]() {
		t.Fatalf("Service.flagRec is a %v, want FlagRecorder", f.Type)
	}
	set, ok := reflect.TypeFor[*Service]().MethodByName("SetFlagRecorder")
	if !ok || set.Type.NumIn() != 2 || set.Type.In(1) != reflect.TypeFor[FlagRecorder]() {
		t.Fatalf("SetFlagRecorder = %v, want func(*Service, FlagRecorder)", set.Type)
	}
}
