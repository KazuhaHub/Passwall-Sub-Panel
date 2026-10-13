//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type nativeCoreFreeze struct {
	observed chan struct{}
	mu       sync.Mutex
	fd       int
	release  func()
}

func (f *nativeCoreFreeze) exited() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fd >= 0 && errors.Is(unix.PidfdSendSignal(f.fd, 0, nil, 0), unix.ESRCH)
}

// Bind the fault to the installed executable serving this Node and one exact
// candidate check. pidfd prevents PID reuse from redirecting either signal.
// Neither the live core, emitted config nor production timeout is changed.
func nativePauseNextCoreCheck(t *testing.T, parent int, dataDir string) *nativeCoreFreeze {
	t.Helper()
	stateDir := filepath.Join(dataDir, "runtime", "xray")
	var binary string
	var owned os.FileInfo
	for _, child := range nativeCoreChildren(parent) {
		args := nativeCoreArgs(child)
		if len(args) == 0 || !nativeOwnedXrayCommand(args, args[0], stateDir, false) {
			continue
		}
		rel, err := filepath.Rel(filepath.Join(dataDir, "cores"), args[0])
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		file, err := os.Stat(args[0])
		executable, exeErr := os.Stat(filepath.Join("/proc", strconv.Itoa(child), "exe"))
		if err == nil && exeErr == nil && file.Mode().IsRegular() && os.SameFile(file, executable) {
			binary, owned = args[0], file
			break
		}
	}
	if binary == "" {
		t.Fatal("cannot bind timeout fault to the owned running core")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	f := &nativeCoreFreeze{observed: make(chan struct{}), fd: -1}
	done := make(chan struct{})
	var held []int
	var released sync.Once
	f.release = func() {
		released.Do(func() {
			cancel()
			<-done
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, fd := range held {
				if err := unix.PidfdSendSignal(fd, unix.SIGCONT, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
					t.Error("cannot resume owned core check during cleanup")
				}
				_ = unix.Close(fd)
			}
			f.fd = -1
		})
	}
	t.Cleanup(f.release)
	go func() {
		defer close(done)
		paused := map[int]int{}
		for ctx.Err() == nil {
			if len(held) >= 8 {
				return
			}
			for _, child := range nativeCoreChildren(parent) {
				if fd, ok := paused[child]; ok && !errors.Is(unix.PidfdSendSignal(fd, 0, nil, 0), unix.ESRCH) {
					continue
				}
				if !nativeCoreHasParent(child, parent) || !nativeOwnedXrayCommand(nativeCoreArgs(child), binary, stateDir, true) {
					continue
				}
				fd, err := unix.PidfdOpen(child, 0)
				if err != nil {
					continue
				}
				executable, err := os.Stat(filepath.Join("/proc", strconv.Itoa(child), "exe"))
				if err != nil || !os.SameFile(owned, executable) || unix.PidfdSendSignal(fd, unix.SIGSTOP, nil, 0) != nil {
					_ = unix.Close(fd)
					continue
				}
				// Check identity again after stopping, while the pidfd is held.
				if !nativeCoreHasParent(child, parent) || !nativeOwnedXrayCommand(nativeCoreArgs(child), binary, stateDir, true) {
					_ = unix.PidfdSendSignal(fd, unix.SIGCONT, nil, 0)
					_ = unix.Close(fd)
					continue
				}
				f.mu.Lock()
				held = append(held, fd)
				paused[child] = fd
				if f.fd == -1 {
					f.fd = fd
					close(f.observed)
				}
				f.mu.Unlock()
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	return f
}

func nativeCoreArgs(pid int) []string {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil || len(raw) == 0 || len(raw) > 8192 {
		return nil
	}
	return strings.Split(string(bytes.TrimSuffix(raw, []byte{0})), "\x00")
}

func nativeCoreHasParent(pid, parent int) bool {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "PPid:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "PPid:")) == strconv.Itoa(parent)
		}
	}
	return false
}

func nativeCoreChildren(parent int) []int {
	tasks, err := os.ReadDir(filepath.Join("/proc", strconv.Itoa(parent), "task"))
	if err != nil {
		return nil
	}
	seen := map[int]bool{}
	var result []int
	for _, task := range tasks {
		raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(parent), "task", task.Name(), "children"))
		if err != nil {
			continue
		}
		for _, child := range strings.Fields(string(raw)) {
			pid, err := strconv.Atoi(child)
			if err == nil && pid > 0 && !seen[pid] {
				seen[pid] = true
				result = append(result, pid)
			}
		}
	}
	return result
}
