//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destaudit"
)

// This lifecycle hold leaves the production service, admission callbacks,
// producers, queue limits and eventual SQL worker intact. The cohort driver
// must release it before shutdown, including on a failed setup.
type nativeGlobalAuditStart struct {
	destinationAuditRunner
	observer interface {
		Pressure() [4]destaudit.LanePressure
	}
	ready chan struct{}
	once  sync.Once
}

func newNativeGlobalAuditStart(original destinationAuditRunner) (*nativeGlobalAuditStart, error) {
	observer, ok := original.(interface {
		Pressure() [4]destaudit.LanePressure
	})
	if !ok || original == nil {
		return nil, errors.New("global fixture lacks the production audit observer")
	}
	return &nativeGlobalAuditStart{destinationAuditRunner: original, observer: observer, ready: make(chan struct{})}, nil
}

func (g *nativeGlobalAuditStart) release() { g.once.Do(func() { close(g.ready) }) }

func (g *nativeGlobalAuditStart) Run(ctx context.Context) destaudit.Summary {
	select {
	case <-g.ready:
	case <-ctx.Done():
	}
	return g.destinationAuditRunner.Run(ctx)
}

func (g *nativeGlobalAuditStart) Pressure() [4]destaudit.LanePressure {
	return g.observer.Pressure()
}

func TestNativeGlobalAuditStartPreservesRealQueueAndPermissionChanges(t *testing.T) {
	// Production App/SQLite/authorization integration, using protocol vectors.
	// This is not evidence of the future real multi-Node traffic cohort.
	f := buildDestinationPolicyFixture(t)
	gate, err := newNativeGlobalAuditStart(f.a.destAudit)
	nativeMust(t, err, "bind the actual production audit lifecycle")
	if owners, valid := nativeGlobalOwners(gate.Pressure()); !valid || owners != 65 {
		t.Fatal("global cohort does not cover actual reserved lane slots")
	}
	f.a.destAudit = gate
	f.a.startDestinationAudit()
	t.Cleanup(gate.release) // release before the fixture's real shutdown
	f.report.Capabilities = []string{protocol.CapabilityAuditHits}
	f.report.CoreEngine = "xray"
	f.report.Audit = fixtureAudit(t, f, 1)
	response := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	pressure := gate.Pressure()
	if response.Config.Body == nil || response.Roster.Body == nil || pressure[0].Batches != 1 || pressure[0].Active != 0 || pressure[0].Bytes <= 0 || auditTableCount(t, f.a, "dest_hits") != 0 {
		t.Fatal("held worker changed production control or immediate queue admission")
	}
	token := destinationRefreshAdminToken(t, f.a)
	for _, mode := range []string{"off", "hits"} {
		result := serverAuditRequest(t, f.a, token, "PUT", fmt.Sprintf("/%d", f.agent.PanelID), map[string]any{"audit_collect": mode})
		if result.Code != 200 {
			t.Fatal("real collection mutation did not notify the held service")
		}
	}
	_ = syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if gate.Pressure()[0].Batches != 0 {
		t.Fatal("held worker resurrected canceled or stale collection data")
	}
	f.report.Audit = fixtureAudit(t, f, 2)
	f.report.Audit.CollectRevision = 3
	_ = syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if gate.Pressure()[0].Batches != 1 {
		t.Fatal("held worker lost current production admission")
	}
	gate.release()
	gate.release() // safe cleanup after explicit release
	waitAuditRows(t, f.a, "dest_hits", 1)
	gate.StopOffers()
	f.a.bgWG.Wait()
	var count int64
	nativeMust(t, f.a.database.QueryRowContext(t.Context(), "SELECT count FROM dest_hits").Scan(&count), "read actual released worker transaction")
	if count != 4 || gate.Pressure()[0].Batches != 0 {
		t.Fatal("released original SQL worker did not finish exactly once")
	}
}

// One more than the most demanding lane's owner count exercises a refusal
// beyond all reserved batch slots. Derive it from the actual queue observer.
func nativeGlobalOwners(lanes [4]destaudit.LanePressure) (int, bool) {
	owners := 0
	for i, lane := range lanes {
		if lane.Kind != nativeMixedKinds[i] || lane.BatchLimit <= 0 || lane.OwnerLimit <= 0 || lane.BatchLimit%lane.OwnerLimit != 0 {
			return 0, false
		}
		owners = max(owners, lane.BatchLimit/lane.OwnerLimit)
	}
	return owners + 1, owners > 0
}

type nativePublicCoreMetadata struct {
	Schema      int       `json:"schema_version"`
	Engine      string    `json:"engine"`
	Version     string    `json:"version"`
	Source      string    `json:"source_url"`
	ArchiveSHA  string    `json:"archive_sha256"`
	BinarySHA   string    `json:"binary_sha256"`
	InstalledAt time.Time `json:"installed_at"`
}

// Called only after the first real owned core has booted and been bound. Copy
// its two public installer files, never state.db, runtime, credentials or logs.
// Each subsequent real Node still performs its normal catalog/digest/version
// verification. No installation metadata or core bytes are synthesized here.
func nativeCopyPublicCoreCache(sourceData, targetData string) error {
	fail := func() error { return errors.New("global public core copy rejected; private paths withheld") }
	if !filepath.IsAbs(sourceData) || !filepath.IsAbs(targetData) || filepath.Base(sourceData) != "node" || filepath.Base(targetData) != "node" || sourceData == targetData {
		return fail()
	}
	resolved, err := filepath.EvalSymlinks(sourceData)
	if err != nil || resolved != sourceData {
		return fail()
	}
	resolved, err = filepath.EvalSymlinks(filepath.Dir(targetData))
	if err != nil || resolved != filepath.Dir(targetData) {
		return fail()
	}
	if _, err := os.Lstat(targetData); !errors.Is(err, os.ErrNotExist) {
		return fail()
	}
	rel := filepath.Join("cores", "xray", "versions", "26.6.27")
	source, target := filepath.Join(sourceData, rel), filepath.Join(targetData, rel)
	resolved, err = filepath.EvalSymlinks(source)
	if err != nil || resolved != source {
		return fail()
	}
	entries, err := os.ReadDir(source)
	if err != nil || len(entries) != 2 || entries[0].Name() != "install.json" || entries[1].Name() != "xray" {
		return fail()
	}
	for _, entry := range entries {
		info, err := os.Lstat(filepath.Join(source, entry.Name()))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
			return fail()
		}
	}
	info, err := os.Stat(filepath.Join(source, "install.json"))
	if err != nil || info.Size() <= 0 || info.Size() > 8192 {
		return fail()
	}
	data, err := os.ReadFile(filepath.Join(source, "install.json"))
	if err != nil {
		return fail()
	}
	var meta nativePublicCoreMetadata
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&meta) != nil || decoder.Decode(new(any)) != io.EOF || meta.Schema != 1 || meta.Engine != "xray" || meta.Version != "26.6.27" || meta.InstalledAt.IsZero() || meta.Source != "https://github.com/XTLS/Xray-core/releases/download/v26.6.27/Xray-linux-64.zip" {
		return fail()
	}
	for _, digest := range []string{meta.ArchiveSHA, meta.BinarySHA} {
		if decoded, err := hex.DecodeString(digest); err != nil || len(decoded) != sha256.Size || strings.ToLower(digest) != digest {
			return fail()
		}
	}
	input, err := os.Open(filepath.Join(source, "xray"))
	if err != nil {
		return fail()
	}
	defer input.Close()
	info, err = input.Stat()
	if err != nil || info.Size() <= 0 || info.Size() > 512<<20 || info.Mode().Perm()&0111 == 0 {
		return fail()
	}
	if os.MkdirAll(target, 0700) != nil {
		return fail()
	}
	output, err := os.OpenFile(filepath.Join(target, "xray"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		return fail()
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(input, (512<<20)+1))
	syncErr, closeErr := output.Sync(), output.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || written != info.Size() || hex.EncodeToString(hash.Sum(nil)) != meta.BinarySHA {
		return fail()
	}
	file, err := os.OpenFile(filepath.Join(target, "install.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fail()
	}
	_, writeErr := file.Write(data)
	syncErr, closeErr = file.Sync(), file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return fail()
	}
	return nil
}

func TestNativeGlobalPublicCoreCopyExcludesStateAndRejectsUntrustedFiles(t *testing.T) {
	// Synthetic file-integrity vectors only; no fake core is executed.
	makeSource := func() (string, []byte) {
		root := filepath.Join(t.TempDir(), "node")
		dir := filepath.Join(root, "cores", "xray", "versions", "26.6.27")
		nativeMust(t, os.MkdirAll(dir, 0700), "make synthetic public cache")
		binary := []byte("synthetic executable digest vector; not a running core")
		digest := sha256.Sum256(binary)
		meta := nativePublicCoreMetadata{Schema: 1, Engine: "xray", Version: "26.6.27", Source: "https://github.com/XTLS/Xray-core/releases/download/v26.6.27/Xray-linux-64.zip", ArchiveSHA: strings.Repeat("a", 64), BinarySHA: hex.EncodeToString(digest[:]), InstalledAt: time.Now().UTC()}
		body, err := json.Marshal(meta)
		nativeMust(t, err, "encode synthetic public cache")
		nativeMust(t, os.WriteFile(filepath.Join(dir, "xray"), binary, 0700), "write synthetic digest vector")
		nativeMust(t, os.WriteFile(filepath.Join(dir, "install.json"), body, 0600), "write synthetic public metadata")
		for _, name := range []string{"state.db", "credential", "runtime", "private.log"} {
			nativeMust(t, os.WriteFile(filepath.Join(root, name), []byte("private sentinel"), 0600), "seed private exclusion vector")
		}
		return root, binary
	}
	source, binary := makeSource()
	target := filepath.Join(t.TempDir(), "node")
	nativeMust(t, nativeCopyPublicCoreCache(source, target), "copy only synthetic public installer files")
	copied, err := os.ReadFile(filepath.Join(target, "cores", "xray", "versions", "26.6.27", "xray"))
	nativeMust(t, err, "read copied public digest vector")
	if !bytes.Equal(copied, binary) {
		t.Fatal("public copy changed core bytes")
	}
	for _, name := range []string{"state.db", "credential", "runtime", "private.log"} {
		if _, err := os.Lstat(filepath.Join(target, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("public copy included private node state")
		}
	}
	if nativeCopyPublicCoreCache(source, target) == nil {
		t.Fatal("public copy overwrote an existing target")
	}
	for _, mutation := range []string{"digest", "metadata", "symlink", "extra"} {
		t.Run(mutation, func(t *testing.T) {
			src, _ := makeSource()
			dir := filepath.Join(src, "cores", "xray", "versions", "26.6.27")
			switch mutation {
			case "digest":
				nativeMust(t, os.WriteFile(filepath.Join(dir, "xray"), []byte("changed"), 0700), "change synthetic digest vector")
			case "metadata":
				nativeMust(t, os.WriteFile(filepath.Join(dir, "install.json"), []byte(`{"schema_version":1,"private":"sentinel"}`), 0600), "change synthetic metadata vector")
			case "symlink":
				nativeMust(t, os.Remove(filepath.Join(dir, "xray")), "replace owned synthetic vector")
				nativeMust(t, os.Symlink(filepath.Join(src, "credential"), filepath.Join(dir, "xray")), "seed private symlink vector")
			case "extra":
				nativeMust(t, os.WriteFile(filepath.Join(dir, "state.db"), []byte("private"), 0600), "seed unexpected private cache file")
			}
			if nativeCopyPublicCoreCache(src, filepath.Join(t.TempDir(), "node")) == nil {
				t.Fatal("public copy admitted an untrusted or private cache")
			}
		})
	}
}
