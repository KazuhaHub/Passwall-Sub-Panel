//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"golang.org/x/sys/unix"
)

// Bind to the original owned process without sending it a signal or changing it.
func nativeBindLiveCore(t *testing.T, parent int, dataDir, engine string) func() {
	t.Helper()
	stateDir := filepath.Join(dataDir, "runtime", "xray")
	pid, fd := 0, -1
	var executable os.FileInfo
	for _, child := range nativeCoreChildren(parent) {
		args := nativeCoreArgs(child)
		if !nativeCoreHasParent(child, parent) || !nativeOwnedLiveCoreCommand(args, engine, stateDir) {
			continue
		}
		rel, err := filepath.Rel(filepath.Join(dataDir, "cores"), args[0])
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		file, err := os.Stat(args[0])
		live, liveErr := os.Stat(filepath.Join("/proc", strconv.Itoa(child), "exe"))
		if err != nil || liveErr != nil || !file.Mode().IsRegular() || !os.SameFile(file, live) {
			continue
		}
		held, err := unix.PidfdOpen(child, 0)
		if err != nil {
			continue
		}
		if fd >= 0 {
			_ = unix.Close(held)
			_ = unix.Close(fd)
			t.Fatal("idle sync cannot bind a unique owned live core")
		}
		pid, fd, executable = child, held, file
	}
	if fd < 0 {
		t.Fatal("idle sync cannot bind a unique owned live core")
	}
	t.Cleanup(func() { _ = unix.Close(fd) })
	live := func() {
		t.Helper()
		file, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
		if unix.PidfdSendSignal(fd, 0, nil, 0) != nil || err != nil || !os.SameFile(file, executable) || !nativeCoreHasParent(pid, parent) || !nativeOwnedLiveCoreCommand(nativeCoreArgs(pid), engine, stateDir) {
			t.Fatal("idle sync replaced or lost the original core process")
		}
	}
	return live
}

// Hold the original process identity across actual accepted sync cycles. A PID
// number or an unchanged config alone cannot prove that the core never restarted.
func nativeAssertIdleSync(t *testing.T, a *App, probe *nativeRestartProbe, agentID string, nodeID int64, parent int, dataDir, engine string, traffic func(), mutations ...func()) {
	t.Helper()
	live := nativeBindLiveCore(t, parent, dataDir, engine)
	stateDir := filepath.Join(dataDir, "runtime", "xray")
	live()
	configPath := filepath.Join(stateDir, "current.json")
	config, err := os.ReadFile(configPath)
	nativeMust(t, err, "read idle confirmed config")
	if len(config) == 0 {
		t.Fatal("idle sync has no confirmed core config")
	}
	digest := sha256.Sum256(config)
	prior, err := a.repos.DestAgentPolicy.Get(t.Context(), agentID, true)
	nativeMust(t, err, "read idle confirmed policy")
	agent, err := a.repos.NodeAgent.GetByAgentID(t.Context(), agentID)
	nativeMust(t, err, "read idle agent")
	streams := make([]*domain.NodeAgentStream, 0, 2)
	for _, name := range []domain.NodeAgentStreamName{domain.NodeAgentStreamConfig, domain.NodeAgentStreamRoster} {
		stream, err := a.repos.NodeAgent.GetStream(t.Context(), agentID, name)
		nativeMust(t, err, "read idle confirmed stream")
		streams = append(streams, stream)
	}
	for _, mutate := range mutations {
		mutate()
	}
	for range 3 {
		after := time.Now().UTC()
		probe.arm()
		nativeWait(t, 30*time.Second, "unchanged accepted idle sync", func() bool {
			live()
			first, seen, accepted := probe.first()
			if !seen {
				return false
			}
			if !accepted || first == nil || first.State != "applied" || first.Digest != prior.MintedSHA256 || first.IssueCode != "" {
				t.Fatal("idle sync did not accept the unchanged applied policy")
			}
			currentAgent, err := a.repos.NodeAgent.GetByAgentID(t.Context(), agentID)
			if err != nil || currentAgent.Epoch != agent.Epoch || string(currentAgent.ObservedCoreEngine) != engine {
				return false
			}
			state, err := a.destDefinitions.State(t.Context())
			if err != nil {
				return false
			}
			current, err := a.repos.DestAgentPolicy.Get(t.Context(), agentID, true)
			if err != nil || !nativeUnchangedReceiptMatches(*prior, *current, state.PublishedGeneration, currentAgent.LastSeen, after) {
				return false
			}
			for _, before := range streams {
				now, err := a.repos.NodeAgent.GetStream(t.Context(), agentID, before.Stream)
				if err != nil || !nativeIdleStreamMatches(*before, *now, agent.Epoch, after) {
					return false
				}
			}
			listener, err := a.repos.Node.GetByID(t.Context(), nodeID)
			return err == nil && listener.ConfigSyncState == domain.ConfigSyncSynced
		})
		now, err := os.ReadFile(configPath)
		nativeMust(t, err, "read idle unchanged core config")
		if !bytes.Equal(config, now) || sha256.Sum256(now) != digest {
			t.Fatal("idle sync changed the confirmed core config")
		}
		traffic()
		live()
	}
}
