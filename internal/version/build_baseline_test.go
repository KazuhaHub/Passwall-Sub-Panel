package version

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A dependency's preferred toolchain does not select PSP's compiler. Keep
// source builds and CI on PSP's exact preferred compiler, and require the two
// runtime-image paths to share an exact patch release rather than floating.
func TestPSPBuildBaselinesStayAligned(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("..", "..", path))
		if err != nil {
			t.Fatalf("read build baseline %s: %v", path, err)
		}
		return string(raw)
	}
	toolchains := regexp.MustCompile(`(?m)^toolchain go(\d+\.\d+\.\d+)$`).FindAllStringSubmatch(read("go.mod"), -1)
	if len(toolchains) != 1 {
		t.Fatal("go.mod must select one exact preferred compiler patch release")
	}
	source, release := read("Dockerfile"), read("Dockerfile.release")
	builder := regexp.MustCompile(`(?m)^FROM golang:(\d+\.\d+\.\d+)-alpine(?:\d+\.\d+(?:\.\d+)?)? AS go-builder$`).FindAllStringSubmatch(source, -1)
	if len(builder) != 1 || builder[0][1] != toolchains[0][1] {
		t.Errorf("source Docker compiler must select exactly Go %s with a canonical Alpine variant", toolchains[0][1])
	}
	patchedBase := regexp.MustCompile(`(?m)^FROM alpine:(\d+\.\d+\.\d+)$`)
	sourceBase, releaseBase := patchedBase.FindAllStringSubmatch(source, -1), patchedBase.FindAllStringSubmatch(release, -1)
	if len(sourceBase) != 1 || len(releaseBase) != 1 || sourceBase[0][1] != releaseBase[0][1] {
		t.Error("source and release Docker runtime bases must share one exact three-component Alpine patch release")
	}
	for _, path := range []string{".github/workflows/test.yml", ".github/workflows/release.yml"} {
		t.Run(path, func(t *testing.T) {
			if err := validateBuildBaselineWorkflow(read(path)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type buildBaselineWorkflow struct {
	Env  map[string]string `yaml:"env"`
	Jobs map[string]struct {
		Env   map[string]string   `yaml:"env"`
		Steps []buildBaselineStep `yaml:"steps"`
	} `yaml:"jobs"`
}

type buildBaselineStep struct {
	Uses string            `yaml:"uses"`
	With map[string]string `yaml:"with"`
	Env  map[string]string `yaml:"env"`
	If   string            `yaml:"if"`
	Run  string            `yaml:"run"`
}

// setup-go v6 consults GOTOOLCHAIN while choosing between the go and toolchain
// directives. Only that action may use auto; following shell commands inherit
// local and immediately verify both the host and compiler against go.mod.
const buildBaselineCompilerGate = `build_toolchain=$(awk '$1 == "toolchain" { print $2 }' go.mod)
test -n "$build_toolchain"
test "$(go env GOVERSION)" = "$build_toolchain"
test "$(go tool compile -V | awk '{ print $3 }')" = "$build_toolchain"`

func validateBuildBaselineWorkflow(raw string) error {
	var workflow buildBaselineWorkflow
	if err := yaml.Unmarshal([]byte(raw), &workflow); err != nil {
		return fmt.Errorf("parse build workflow: %w", err)
	}
	if workflow.Env["GOWORK"] != "off" || workflow.Env["GOTOOLCHAIN"] != "local" {
		return fmt.Errorf("workflow must globally disable workspace replacements and automatic toolchain switching")
	}
	jobs := make([]string, 0, len(workflow.Jobs))
	for name := range workflow.Jobs {
		jobs = append(jobs, name)
	}
	sort.Strings(jobs)
	setups := 0
	for _, name := range jobs {
		job := workflow.Jobs[name]
		for key, want := range map[string]string{"GOWORK": "off", "GOTOOLCHAIN": "local"} {
			if value, exists := job.Env[key]; exists && value != want {
				return fmt.Errorf("job %s overrides %s=%s", name, key, want)
			}
		}
		for index, step := range job.Steps {
			setup := strings.HasPrefix(step.Uses, "actions/setup-go@")
			if value, exists := step.Env["GOWORK"]; exists && value != "off" {
				return fmt.Errorf("job %s step %d enables workspace replacements", name, index)
			}
			if !setup {
				if value, exists := step.Env["GOTOOLCHAIN"]; exists && value != "local" {
					return fmt.Errorf("job %s step %d enables automatic shell toolchain switching", name, index)
				}
				continue
			}
			setups++
			if step.Uses != "actions/setup-go@v6" || step.Env["GOTOOLCHAIN"] != "auto" ||
				step.With["go-version-file"] != "go.mod" || step.With["go-version"] != "" || step.If != "" {
				return fmt.Errorf("job %s setup-go must unconditionally select go.mod's preferred toolchain using v6 and action-local auto", name)
			}
			if index+1 >= len(job.Steps) {
				return fmt.Errorf("job %s has no actual compiler verification after setup-go", name)
			}
			gate := job.Steps[index+1]
			if gate.Uses != "" || gate.If != "" || !hasBuildBaselineCompilerGate(gate.Run) {
				return fmt.Errorf("job %s must immediately and unconditionally verify host and compiler against go.mod", name)
			}
		}
	}
	if setups == 0 {
		return fmt.Errorf("build workflow has no pinned Go setup")
	}
	return nil
}

func hasBuildBaselineCompilerGate(run string) bool {
	var lines []string
	for _, line := range strings.Split(run, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return strings.Contains(strings.Join(lines, "\n"), buildBaselineCompilerGate)
}

func TestBuildBaselineWorkflowGuardRejectsCompilerDrift(t *testing.T) {
	fixture := `env:
  GOWORK: "off"
  GOTOOLCHAIN: "local"
jobs:
  build:
    steps:
      - uses: actions/setup-go@v6
        env:
          GOTOOLCHAIN: auto
        with:
          go-version-file: go.mod
      - name: Verify exact preferred compiler
        run: |
` + "          " + strings.ReplaceAll(buildBaselineCompilerGate, "\n", "\n          ") + "\n"
	if err := validateBuildBaselineWorkflow(fixture); err != nil {
		t.Fatalf("canonical compiler workflow failed: %v", err)
	}
	cases := map[string]string{
		"floating compiler":            strings.Replace(fixture, "go-version-file: go.mod", "go-version: 1.26.x", 1),
		"old setup-go":                 strings.Replace(fixture, "actions/setup-go@v6", "actions/setup-go@v5", 1),
		"minimum instead of preferred": strings.Replace(fixture, "GOTOOLCHAIN: auto", "GOTOOLCHAIN: local", 1),
		"automatic shell switching":    strings.Replace(fixture, `GOTOOLCHAIN: "local"`, `GOTOOLCHAIN: "auto"`, 1),
		"workspace replacement":        strings.Replace(fixture, `GOWORK: "off"`, `GOWORK: "auto"`, 1),
		"missing host assertion":       strings.Replace(fixture, `test "$(go env GOVERSION)" = "$build_toolchain"`, `echo "$(go env GOVERSION)"`, 1),
		"missing compiler assertion":   strings.Replace(fixture, `test "$(go tool compile -V | awk '{ print $3 }')" = "$build_toolchain"`, `echo "$(go tool compile -V)"`, 1),
		"conditional assertion":        strings.Replace(fixture, "      - name: Verify exact preferred compiler", "      - name: Verify exact preferred compiler\n        if: false", 1),
		"job override":                 strings.Replace(fixture, "  build:\n", "  build:\n    env:\n      GOTOOLCHAIN: auto\n", 1),
		"gate override":                strings.Replace(fixture, "        run: |", "        env:\n          GOTOOLCHAIN: auto\n        run: |", 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateBuildBaselineWorkflow(raw); err == nil {
				t.Fatal("compiler drift must fail the workflow guard")
			}
		})
	}
}
