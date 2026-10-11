package app

import (
	"path/filepath"
	"strings"
	"testing"
)

func nativeOwnedXrayCommand(args []string, binary, stateDir string, check bool) bool {
	if !filepath.IsAbs(binary) || !filepath.IsAbs(stateDir) || len(args) < 4 || args[0] != binary || args[1] != "run" {
		return false
	}
	config := ""
	if check {
		if len(args) != 5 || args[2] != "-test" || args[3] != "-config" {
			return false
		}
		config = args[4]
		name := filepath.Base(config)
		if !strings.HasPrefix(name, ".candidate-") || !strings.HasSuffix(name, ".json") || len(name) <= len(".candidate-.json") {
			return false
		}
	} else {
		if len(args) != 4 || args[2] != "-config" || filepath.Base(args[3]) != "current.json" {
			return false
		}
		config = args[3]
	}
	return filepath.IsAbs(config) && filepath.Clean(config) == config && filepath.Dir(config) == stateDir
}

func TestNativeCoreFaultSelectsOnlyTheOwnedCheck(t *testing.T) {
	root := t.TempDir()
	binary, stateDir := filepath.Join(root, "xray"), filepath.Join(root, "runtime", "xray")
	candidate := filepath.Join(stateDir, ".candidate-123.json")
	for _, tc := range []struct {
		name        string
		args        []string
		check, want bool
	}{
		{"check", []string{binary, "run", "-test", "-config", candidate}, true, true},
		{"live", []string{binary, "run", "-config", filepath.Join(stateDir, "current.json")}, false, true},
		{"never-stop-live", []string{binary, "run", "-config", filepath.Join(stateDir, "current.json")}, true, false},
		{"other-binary", []string{filepath.Join(root, "other"), "run", "-test", "-config", candidate}, true, false},
		{"other-directory", []string{binary, "run", "-test", "-config", filepath.Join(root, ".candidate-123.json")}, true, false},
		{"current-not-candidate", []string{binary, "run", "-test", "-config", filepath.Join(stateDir, "current.json")}, true, false},
		{"extra-argument", []string{binary, "run", "-test", "-config", candidate, "other"}, true, false},
		{"missing-argument", []string{binary, "run", "-test", "-config"}, true, false},
		{"relative-binary", []string{"xray", "run", "-test", "-config", candidate}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nativeOwnedXrayCommand(tc.args, binary, stateDir, tc.check); got != tc.want {
				t.Fatalf("owned core selection = %t, want %t", got, tc.want)
			}
		})
	}
}
