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
	return nativeSetupProfileOutput(root, "cpu")
}

func nativeSetupProfileOutput(root, kind string) (*os.File, error) {
	if kind != "cpu" && kind != "alloc-before" && kind != "alloc-after" {
		return nil, os.ErrInvalid
	}
	if root == "" {
		return nil, os.ErrInvalid
	}
	dir, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, "destination-global-setup."+kind+".pprof"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}

func nativeWriteSetupAllocations(root, kind string) error {
	file, err := nativeSetupProfileOutput(root, kind)
	if err != nil {
		return err
	}
	writeErr := pprof.Lookup("allocs").WriteTo(file, 0)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

type nativeSetupProfile struct {
	stop func()
	done <-chan struct{}
}

func nativeStartSetupProfile(t *testing.T, root string, duration time.Duration) nativeSetupProfile {
	t.Helper()
	// Cumulative allocation profiles are differenced by pprof in the workflow.
	// Keep the runtime's normal sampling/GC cadence; do not force a collection.
	nativeMust(t, nativeWriteSetupAllocations(root, "alloc-before"), "save setup allocation baseline")
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
			if err := nativeWriteSetupAllocations(root, "alloc-after"); err != nil {
				t.Error("save setup allocation endpoint")
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
	for _, kind := range []string{"alloc-before", "alloc-after"} {
		root := t.TempDir()
		file, err := nativeSetupProfileOutput(root, kind)
		nativeMust(t, err, "create private allocation profile fixture")
		nativeMust(t, file.Close(), "close allocation profile fixture")
		if info, err := os.Stat(file.Name()); err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("allocation profile file must be private")
		}
		if err := nativeWriteSetupAllocations(root, kind); !os.IsExist(err) {
			t.Fatal("allocation profile must not overwrite existing evidence")
		}
		linked := t.TempDir()
		nativeMust(t, os.Symlink(file.Name(), filepath.Join(linked, filepath.Base(file.Name()))), "create allocation link rejection fixture")
		if err := nativeWriteSetupAllocations(linked, kind); !os.IsExist(err) {
			t.Fatal("allocation profile must not follow an existing file link")
		}
	}
	if _, err := nativeSetupProfileOutput(t.TempDir(), "../outside"); err == nil {
		t.Fatal("profile filename selection must be closed")
	}
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
	for _, kind := range []string{"cpu", "alloc-before", "alloc-after"} {
		data, err := os.ReadFile(filepath.Join(root, "destination-global-setup."+kind+".pprof"))
		if err != nil || len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
			t.Fatal("finished profiles must be readable gzip streams")
		}
	}
	profile.stop()
	profile.stop()
	second := nativeStartSetupProfile(t, t.TempDir(), time.Minute)
	second.stop()
	second.stop()
}
