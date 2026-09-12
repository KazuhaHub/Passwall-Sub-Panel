package idgen

import (
	"bytes"
	"errors"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"
)

var canonicalTaskID = regexp.MustCompile(`^tsk1_[0-9a-f]{48}_[0-9a-f]{16}$`)

func TestTaskIDMinterFormatAndProtocolCompatibility(t *testing.T) {
	entropy := bytes.NewReader(bytes.Repeat([]byte{0xab}, taskIDIssuerBytes+1))
	minter, err := newTaskIDMinter(entropy)
	if err != nil {
		t.Fatal(err)
	}
	if entropy.Len() != 1 {
		t.Fatalf("constructor consumed %d excess entropy bytes, want none", 1-entropy.Len())
	}
	for counter := uint64(1); counter <= 2; counter++ {
		id, err := minter.Next()
		if err != nil {
			t.Fatal(err)
		}
		want := "tsk1_" + strings.Repeat("ab", taskIDIssuerBytes) + "_000000000000000" + strconv.FormatUint(counter, 16)
		if id != want {
			t.Fatalf("ID = %q, want %q", id, want)
		}
		assertCanonicalProtocolTaskID(t, id)
	}
}

func TestTaskIDMinterFreshInstancesHaveDifferentIssuers(t *testing.T) {
	issuers := make(map[string]struct{})
	for i := 0; i < 32; i++ {
		minter, err := NewTaskIDMinter()
		if err != nil {
			t.Fatal(err)
		}
		id, err := minter.Next()
		if err != nil {
			t.Fatal(err)
		}
		assertCanonicalProtocolTaskID(t, id)
		issuer := strings.Split(id, "_")[1]
		if _, exists := issuers[issuer]; exists {
			t.Fatalf("fresh minter reused issuer %q", issuer)
		}
		issuers[issuer] = struct{}{}
	}
}

func TestTaskIDMinterConcurrentIDsAreUniqueAndContiguous(t *testing.T) {
	const callers = 4096
	minter := newDeterministicTaskIDMinter(t)
	type outcome struct {
		id  string
		err error
	}
	start := make(chan struct{})
	results := make(chan outcome, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			id, err := minter.Next()
			results <- outcome{id: id, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	seen := make(map[string]struct{}, callers)
	counters := make(map[uint64]struct{}, callers)
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		assertCanonicalProtocolTaskID(t, result.id)
		if _, exists := seen[result.id]; exists {
			t.Fatalf("duplicate concurrent task ID %q", result.id)
		}
		seen[result.id] = struct{}{}
		counter, err := strconv.ParseUint(result.id[len(result.id)-16:], 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		counters[counter] = struct{}{}
	}
	if len(seen) != callers || minter.counter.Load() != callers {
		t.Fatalf("unique IDs/counter = %d/%d, want %d/%d", len(seen), minter.counter.Load(), callers, callers)
	}
	for counter := uint64(1); counter <= callers; counter++ {
		if _, exists := counters[counter]; !exists {
			t.Fatalf("concurrent counter sequence omitted %d", counter)
		}
	}
}

func TestTaskIDMinterCounterExhaustionNeverWraps(t *testing.T) {
	minter := newDeterministicTaskIDMinter(t)
	minter.counter.Store(math.MaxUint64 - 2)
	for _, suffix := range []string{"fffffffffffffffe", "ffffffffffffffff"} {
		id, err := minter.Next()
		if err != nil || !strings.HasSuffix(id, "_"+suffix) {
			t.Fatalf("boundary ID = (%q, %v), want suffix %q", id, err, suffix)
		}
		assertCanonicalProtocolTaskID(t, id)
	}
	for i := 0; i < 3; i++ {
		if id, err := minter.Next(); id != "" || !errors.Is(err, ErrTaskIDCounterExhausted) {
			t.Fatalf("exhausted Next = (%q, %v), want empty/ErrTaskIDCounterExhausted", id, err)
		}
		if minter.counter.Load() != math.MaxUint64 {
			t.Fatalf("exhausted counter wrapped to %d", minter.counter.Load())
		}
	}
}

func TestTaskIDMinterConcurrentLastCounterHasOneWinner(t *testing.T) {
	const callers = 64
	minter := newDeterministicTaskIDMinter(t)
	minter.counter.Store(math.MaxUint64 - 1)
	results := make(chan error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			id, err := minter.Next()
			if err == nil && !strings.HasSuffix(id, "_ffffffffffffffff") {
				err = errors.New("last counter returned an unexpected ID")
			}
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrTaskIDCounterExhausted):
		default:
			t.Fatal(err)
		}
	}
	if winners != 1 || minter.counter.Load() != math.MaxUint64 {
		t.Fatalf("last-counter winners/counter = %d/%d, want 1/MaxUint64", winners, minter.counter.Load())
	}
}

func TestTaskIDMinterNilAndZeroValuesFailClosed(t *testing.T) {
	var nilMinter *TaskIDMinter
	var zeroMinter TaskIDMinter
	for _, minter := range []*TaskIDMinter{nilMinter, &zeroMinter} {
		if id, err := minter.Next(); id != "" || !errors.Is(err, ErrTaskIDMinterUninitialized) {
			t.Fatalf("uninitialized Next = (%q, %v), want empty/ErrTaskIDMinterUninitialized", id, err)
		}
	}
	if zeroMinter.counter.Load() != 0 {
		t.Fatalf("uninitialized minter advanced counter to %d", zeroMinter.counter.Load())
	}
}

func TestTaskIDMinterConstructorHandlesPartialEntropyReads(t *testing.T) {
	entropy := &taskIDChunkReader{reader: bytes.NewReader(make([]byte, taskIDIssuerBytes)), chunk: 3}
	minter, err := newTaskIDMinter(entropy)
	if err != nil {
		t.Fatal(err)
	}
	if entropy.calls != taskIDIssuerBytes/entropy.chunk {
		t.Fatalf("entropy reads = %d, want %d partial reads", entropy.calls, taskIDIssuerBytes/entropy.chunk)
	}
	id, err := minter.Next()
	if err != nil {
		t.Fatal(err)
	}
	// A complete sample of zero bytes is still a valid entropy-source output;
	// readiness is construction success, not guessing whether bytes look random.
	if want := "tsk1_" + strings.Repeat("0", 48) + "_0000000000000001"; id != want {
		t.Fatalf("partial-read ID = %q, want %q", id, want)
	}
}

func TestTaskIDMinterConstructorEntropyFailuresReturnNoMinter(t *testing.T) {
	entropyFailure := errors.New("entropy unavailable")
	for _, test := range []struct {
		name    string
		entropy io.Reader
		wantErr error
	}{
		{name: "nil"},
		{name: "empty", entropy: bytes.NewReader(nil), wantErr: io.EOF},
		{name: "short", entropy: bytes.NewReader(make([]byte, taskIDIssuerBytes-1)), wantErr: io.ErrUnexpectedEOF},
		{name: "failure", entropy: taskIDErrorReader{err: entropyFailure}, wantErr: entropyFailure},
		{
			name: "partial_then_failure",
			entropy: io.MultiReader(bytes.NewReader(make([]byte, taskIDIssuerBytes-1)),
				taskIDErrorReader{err: entropyFailure}),
			wantErr: entropyFailure,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			minter, err := newTaskIDMinter(test.entropy)
			if minter != nil || err == nil {
				t.Fatalf("constructor = (%+v, %v), want nil/error", minter, err)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("constructor error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func newDeterministicTaskIDMinter(t *testing.T) *TaskIDMinter {
	t.Helper()
	minter, err := newTaskIDMinter(bytes.NewReader(bytes.Repeat([]byte{0x42}, taskIDIssuerBytes)))
	if err != nil {
		t.Fatal(err)
	}
	return minter
}

func assertCanonicalProtocolTaskID(t *testing.T, id string) {
	t.Helper()
	if len(id) != 70 || !canonicalTaskID.MatchString(id) {
		t.Fatalf("non-canonical task ID %q (bytes %d)", id, len(id))
	}
	const kind = "reality_probe.v1"
	args := []byte(`{"target":"example.test:443"}`)
	if err := nodeprotocol.ValidateTasks([]nodeprotocol.Task{{
		ID: id, Kind: kind, Args: args, InputSHA256: nodeprotocol.ComputeTaskInputSHA256(kind, args),
	}}); err != nil {
		t.Fatalf("task ID failed wire validator: %v", err)
	}
}

type taskIDChunkReader struct {
	reader io.Reader
	chunk  int
	calls  int
}

func (r *taskIDChunkReader) Read(p []byte) (int, error) {
	r.calls++
	if len(p) > r.chunk {
		p = p[:r.chunk]
	}
	return r.reader.Read(p)
}

type taskIDErrorReader struct{ err error }

func (r taskIDErrorReader) Read([]byte) (int, error) { return 0, r.err }
