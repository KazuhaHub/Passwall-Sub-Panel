//go:build destination_acceptance && linux

package app

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sync"
	"testing"
	"time"
)

// The profile stays in the owned runner directory. Only pprof's static function
// summary is printed by the workflow; the binary is not an acceptance artifact.
func nativeSetupProfileFile(root string) (*os.File, error) {
	if root == "" {
		return nil, os.ErrInvalid
	}
	dir, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, "destination-global-setup.cpu.pprof"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}

type nativeSetupProfile struct {
	stop func()
	done <-chan struct{}
}

func nativeStartSetupProfile(t *testing.T, root string, duration time.Duration) nativeSetupProfile {
	t.Helper()
	file, err := nativeSetupProfileFile(root)
	nativeMust(t, err, "create exclusive owned setup profile")
	if err := pprof.StartCPUProfile(file); err != nil {
		_ = file.Close()
		t.Fatal("start setup CPU profile")
	}
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	began := time.Now()
	var once sync.Once
	done := make(chan struct{})
	finish := func() {
		once.Do(func() {
			pprof.StopCPUProfile()
			if err := file.Close(); err != nil {
				t.Error("close setup CPU profile")
			}
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			for name, value := range map[string]uint64{
				"elapsed_ms":      uint64(time.Since(began).Milliseconds()),
				"allocated_bytes": after.TotalAlloc - before.TotalAlloc,
				"gc_cycles":       uint64(after.NumGC - before.NumGC),
				"gc_pause_ns":     after.PauseTotalNs - before.PauseTotalNs,
				"heap_bytes":      after.HeapAlloc,
			} {
				t.Logf("native global setup profile: %s=%d", name, value)
			}
			close(done)
		})
	}
	go func() {
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case <-timer.C:
			finish()
		case <-done:
		}
	}()
	return nativeSetupProfile{stop: finish, done: done}
}

func TestNativeSetupProfileRejectsExistingFilesAndLinks(t *testing.T) {
	root := t.TempDir()
	file, err := nativeSetupProfileFile(root)
	nativeMust(t, err, "create owned profile fixture")
	nativeMust(t, file.Close(), "close owned profile fixture")
	if info, err := os.Stat(file.Name()); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("profile file must be private")
	}
	if _, err := nativeSetupProfileFile(root); !os.IsExist(err) {
		t.Fatal("existing evidence must not be overwritten")
	}
	linked := t.TempDir()
	nativeMust(t, os.Symlink(file.Name(), filepath.Join(linked, filepath.Base(file.Name()))), "create symlink rejection fixture")
	if _, err := nativeSetupProfileFile(linked); !os.IsExist(err) {
		t.Fatal("profile must not follow an existing file link")
	}
	if _, err := nativeSetupProfileFile(""); err == nil {
		t.Fatal("profile must have an owned directory")
	}
}

func TestNativeSetupProfileStopsAndClosesOnce(t *testing.T) {
	root := t.TempDir()
	profile := nativeStartSetupProfile(t, root, 20*time.Millisecond)
	t.Cleanup(profile.stop)
	// Exercise the timer first, then two explicit stops. The second profile
	// confirms that the process-wide profiler was released by the first one.
	select {
	case <-profile.done:
	case <-time.After(3 * time.Second):
		t.Fatal("setup profiling must stop on its own deadline")
	}
	data, err := os.ReadFile(filepath.Join(root, "destination-global-setup.cpu.pprof"))
	if err != nil || len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		t.Fatal("finished profile must be a readable gzip stream")
	}
	profile.stop()
	profile.stop()
	second := nativeStartSetupProfile(t, t.TempDir(), time.Minute)
	second.stop()
	second.stop()
}
