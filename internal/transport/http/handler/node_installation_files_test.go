package handler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/audit"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

type nonnativeInstallationPanelRepo struct{ nativeProvisioningPanelRepo }

func (nonnativeInstallationPanelRepo) GetByID(context.Context, int64) (*domain.Panel, error) {
	return &domain.Panel{ID: 41, Kind: domain.PanelKind3XUI}, nil
}

func TestNodeInstallationFilesPrivateStableAcrossMethodsAndPlatforms(t *testing.T) {
	h, repo := installationFixture(t)
	before, credential := *repo.agent, repo.credential
	fileName := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	for _, method := range []string{"docker", "manual"} {
		for _, platform := range []string{"linux", "darwin", "windows"} {
			if method == "docker" && platform != "linux" {
				continue
			}
			for _, arch := range []string{"amd64", "arm64"} {
				t.Run(method+"/"+platform+"/"+arch, func(t *testing.T) {
					body, _ := json.Marshal(nodeInstallationFilesRequest{Version: "v0.0.1-beta3", Method: method, OS: platform, Arch: arch})
					w := installationRequest(h, http.MethodPost, "node-installation-files", string(body), "/panel", domain.RoleAdmin)
					var result nodeInstallationFilesResponse
					if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil {
						t.Fatalf("material generation failed: status=%d", w.Code)
					}
					if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || w.Header().Get("Pragma") != "no-cache" || w.Header().Get("X-Content-Type-Options") != "nosniff" || result.Method != method || result.OS != platform || result.Arch != arch {
						t.Fatal("private response or selected method/platform was lost")
					}
					credentialFiles := 0
					for _, file := range result.Files {
						if !fileName.MatchString(file.Name) || file.Content == "" {
							t.Fatal("unsafe artifact name or empty content")
						}
						if file.Name == "node-credential" {
							credentialFiles++
							if !file.Sensitive || file.Content != credential+"\n" {
								t.Fatal("credential bytes or sensitivity changed")
							}
						} else if strings.Contains(file.Content, credential) || file.Sensitive {
							t.Fatal("credential entered a non-secret configuration artifact")
						}
					}
					if credentialFiles != 1 {
						t.Fatal("expected exactly one private credential artifact")
					}
					for _, step := range result.Steps {
						if step.ID == "" || step.Title == "" || strings.Contains(step.Description, credential) {
							t.Fatal("step identifier/title missing or credential leaked")
						}
						for _, command := range step.Commands {
							if strings.Contains(command, credential) || strings.Contains(command, "--enable-remote-upgrade") || strings.Contains(command, "--run-upgrade-helper") || strings.Contains(command, "systemctl") {
								t.Fatal("manual/container guide leaked credentials or pretended to install systemd")
							}
						}
						assertInstallationCommandSyntax(t, platform, step.Commands)
					}
					if !reflect.DeepEqual(before, *repo.agent) || repo.credential != credential {
						t.Fatal("reading installation materials rebound or rotated the identity")
					}
				})
			}
		}
	}
}

func assertInstallationCommandSyntax(t *testing.T, platform string, commands []string) {
	t.Helper()
	for _, command := range commands {
		if platform != "windows" && runtime.GOOS != "windows" {
			cmd := exec.Command("sh", "-n")
			cmd.Stdin = strings.NewReader(command)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("invalid shell syntax: %v: %s", err, output)
			}
		} else if platform == "windows" && runtime.GOOS == "windows" {
			// Parse only: never run downloads, ACL changes or a daemon in tests.
			cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `$tokens = $null; $errors = $null; [System.Management.Automation.Language.Parser]::ParseInput($env:PSP_INSTALL_PARSE, [ref]$tokens, [ref]$errors) | Out-Null; if ($errors.Count -ne 0) { [Console]::Error.WriteLine(($errors | Out-String)); exit 1 }`)
			cmd.Env = append(os.Environ(), "PSP_INSTALL_PARSE="+command)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("invalid PowerShell syntax: %v: %s", err, output)
			}
		}
	}
}

func TestNodeInstallationFilesRejectsUnsupportedInputsAndNonAdministrators(t *testing.T) {
	h, repo := installationFixture(t)
	for _, body := range []string{
		`{"method":"manual","version":"latest"}`,
		`{"method":"manual","version":"v0.01.0"}`,
		`{"method":"manual","version":"v0.0.1;id"}`,
		`{"method":"manual","version":"v0.0.1","arch":"amd64;id"}`,
		`{"method":"manual","version":"v0.0.1","os":"freebsd"}`,
		`{"method":"docker","version":"v0.0.1","os":"windows"}`,
		`{"method":"systemd","version":"v0.0.1"}`,
		`{"method":"manual","version":" v0.0.1"}`,
		`not-json`,
	} {
		w := installationRequest(h, http.MethodPost, "node-installation-files", body, "", domain.RoleAdmin)
		if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), repo.credential) {
			t.Fatal("unsupported input accepted or leaked a credential")
		}
	}
	for _, role := range []domain.Role{"", domain.RoleUser, domain.RoleOperator} {
		w := installationRequest(h, http.MethodPost, "node-installation-files", `{"method":"manual","version":"v0.0.1-beta3"}`, "", role)
		if (w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden) || strings.Contains(w.Body.String(), repo.credential) {
			t.Fatal("non-administrator could retrieve secret materials")
		}
	}
	before := *repo.agent
	repo.credential = ""
	w := installationRequest(h, http.MethodPost, "node-installation-files", `{"method":"docker","version":"v0.0.1-beta3"}`, "", domain.RoleAdmin)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "node_credential_unavailable") || !reflect.DeepEqual(before, *repo.agent) {
		t.Fatal("legacy credential absence must not mint or rotate a replacement")
	}
}

func TestNodeInstallationMaterialsRequireSuccessfulSecretReadAudit(t *testing.T) {
	for _, action := range []string{"node-install-script", "node-installation-files"} {
		h, repo := installationFixture(t)
		audit := &installationAudit{err: fmt.Errorf("audit unavailable")}
		h.audit = audit
		before, credential := *repo.agent, repo.credential
		body := `{"version":"v0.0.1-beta3","method":"manual"}`
		w := installationRequest(h, http.MethodPost, action, body, "", domain.RoleAdmin)
		if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), credential) || len(audit.entries) != 1 || !reflect.DeepEqual(before, *repo.agent) || repo.credential != credential {
			t.Fatal("failed read audit released credentials or altered identity")
		}
		encoded, _ := json.Marshal(audit.entries)
		if strings.Contains(string(encoded), credential) {
			t.Fatal("read audit contained private materials")
		}
	}
	h, repo := installationFixture(t)
	h.repo = nonnativeInstallationPanelRepo{}
	w := installationRequest(h, http.MethodPost, "node-installation-files", `{"version":"v0.0.1-beta3","method":"manual"}`, "", domain.RoleAdmin)
	if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), repo.credential) {
		t.Fatal("upstream panel received native installation files")
	}
}

func TestNodeInstallationFilesAuditContainsMetadataNotGeneratedSecrets(t *testing.T) {
	h, repo := installationFixture(t)
	entries := &installationAudit{}
	router := gin.New()
	router.Use(middleware.AuditWrites(audit.New(entries), nil))
	router.POST("/api/admin/servers/:id/node-installation-files", h.NodeInstallationFiles)
	request := httptest.NewRequest(http.MethodPost, "https://panel.example/api/admin/servers/41/node-installation-files", strings.NewReader(`{"method":"docker","version":"v0.0.1-beta3"}`))
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, request)
	if w.Code != http.StatusOK || len(entries.entries) != 1 {
		t.Fatal("material generation did not receive write audit metadata")
	}
	encoded, _ := json.Marshal(entries.entries)
	if strings.Contains(string(encoded), repo.credential) || strings.Contains(string(encoded), "compose.yaml") || strings.Contains(string(encoded), "PSP_NODE_ENDPOINT=") {
		t.Fatal("generated response files entered audit records")
	}
}

func TestNodeInstallationFilesDockerIsPinnedAndPreservesLiteralEndpoint(t *testing.T) {
	_, repo := installationFixture(t)
	p := nativeServerCreateResponse{AgentID: repo.agent.AgentID, Credential: repo.credential, Endpoint: "https://panel.example/a$VAR'path/v1/node/sync"}
	result := renderNodeInstallationFiles(41, p, nodeInstallationFilesRequest{Method: "docker", Version: "v0.0.1-beta3", OS: "linux", Arch: "arm64"})
	var compose, env string
	for _, file := range result.Files {
		switch file.Name {
		case "compose.yaml":
			compose = file.Content
		case "node.env":
			env = file.Content
		}
	}
	for _, required := range []string{"name: passwall-node-server-41", "image: ghcr.io/kazuhahub/passwall-node:v0.0.1-beta3", "platform: linux/arm64", "format: raw", "network_mode: host", "file: ./node-credential", "passwall-node-data:/var/lib/passwall-node", "read_only: true", "no-new-privileges:true", "    cap_drop:\n      - ALL\n", "    cap_add:\n      - CHOWN\n      - DAC_OVERRIDE\n      - FOWNER\n      - SETGID\n      - SETUID\n"} {
		if !strings.Contains(compose, required) {
			t.Fatalf("production compose requirement absent: %s", required)
		}
	}
	if strings.Contains(compose, "latest") || strings.Contains(compose, "privileged:") || strings.Contains(compose, "docker.sock") || strings.Contains(compose, p.Endpoint) || !strings.Contains(env, "PSP_NODE_ENDPOINT="+p.Endpoint+"\n") {
		t.Fatal("compose was floating, privileged or interpolated the endpoint")
	}
	if strings.Count(compose, "      - ALL\n") != 1 || !strings.Contains(compose, "permitted/effective capability sets of the non-root daemon") {
		t.Fatal("capabilities were not limited to the documented entrypoint transition")
	}
	var protect string
	for _, step := range result.Steps {
		if step.ID == "credential" {
			protect = strings.Join(step.Commands, "\n")
		}
	}
	if !strings.Contains(protect, "chmod 0600 ./node-credential ./node.env ./compose.yaml") {
		t.Fatal("entrypoint compatibility weakened host credential permissions")
	}
}

func TestNodeInstallationFilesWindowsRequiresArchiveAttributeSupport(t *testing.T) {
	steps := manualWindowsSteps(nativeServerCreateResponse{}, nodeInstallationFilesRequest{Method: "manual", Version: "v0.0.1-beta3", OS: "windows", Arch: "amd64"})
	if !strings.Contains(steps[0].Description, ".NET Framework >=4.7.2") || !strings.Contains(steps[0].Description, "PowerShell 7") {
		t.Fatal("Windows prerequisites do not identify supported archive runtimes")
	}
	var extract string
	for _, step := range steps {
		if step.ID == "extract" {
			extract = strings.Join(step.Commands, "\n")
		}
	}
	guard := "if ($null -eq [IO.Compression.ZipArchiveEntry].GetProperty('ExternalAttributes')) { throw 'Archive member validation requires .NET Framework 4.7.2 or later, or modern PowerShell 7' }"
	guardAt, openAt := strings.Index(extract, guard), strings.Index(extract, "$archive = [IO.Compression.ZipFile]::OpenRead")
	if guardAt < 0 || openAt < 0 || guardAt >= openAt {
		t.Fatal("missing archive attribute support must fail closed before reading entries")
	}
	assertInstallationCommandSyntax(t, "windows", []string{extract})
}

func TestNodeInstallationFilesDockerDefaultsToNativeHostArchitecture(t *testing.T) {
	h, _ := installationFixture(t)
	w := installationRequest(h, http.MethodPost, "node-installation-files", `{"version":"v0.0.1-beta3","method":"docker"}`, "", domain.RoleAdmin)
	var result nodeInstallationFilesResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Arch != "" || strings.Contains(w.Body.String(), `"arch":`) {
		t.Fatal("default Docker guide must not force or report an architecture")
	}
	for _, file := range result.Files {
		if file.Name == "compose.yaml" && strings.Contains(file.Content, "platform:") {
			t.Fatal("default compose forced the amd64 image on an arm64 host")
		}
	}
}

func TestNodeInstallationFilesManualExactArchivesAndSafeQuoting(t *testing.T) {
	_, repo := installationFixture(t)
	p := nativeServerCreateResponse{AgentID: repo.agent.AgentID, Credential: repo.credential, Endpoint: "https://panel.example/a'$(touch-not-a-command)/v1/node/sync"}
	for _, platform := range []string{"linux", "darwin", "windows"} {
		result := renderNodeInstallationFiles(41, p, nodeInstallationFilesRequest{Method: "manual", Version: "v0.0.1-beta3", OS: platform, Arch: "arm64"})
		var download, extract, run string
		for _, step := range result.Steps {
			switch step.ID {
			case "download_verify":
				download = strings.Join(step.Commands, "\n")
			case "extract":
				extract = strings.Join(step.Commands, "\n")
			case "run":
				run = strings.Join(step.Commands, "\n")
			}
			assertInstallationCommandSyntax(t, platform, step.Commands)
		}
		ext := ".tar.gz"
		if platform == "windows" {
			ext = ".zip"
			if !strings.Contains(extract, "FileMode]::CreateNew") || !strings.Contains(extract, "ExternalAttributes") || !strings.Contains(download, "$entries.Count -ne 1") || !strings.Contains(run, "ConvertFrom-Json") {
				t.Fatal("Windows guide did not retain strict checksum/member/data parsing")
			}
		} else if !strings.Contains(run, nodeInstallShellQuote(p.Endpoint)) || !strings.Contains(download, "count != 1") || !strings.Contains(extract, "tar -xOzf") || strings.Contains(extract, "tar -xzf") {
			t.Fatal("Unix guide weakened literal argv or safe member extraction")
		}
		if !strings.Contains(download, "passwall-node_v0.0.1-beta3_"+platform+"_arm64"+ext) || !strings.Contains(download, "https://github.com/KazuhaHub/Passwall-Node/releases/download/v0.0.1-beta3/") || !strings.Contains(download, "--proto-redir '=https'") || !strings.Contains(run, "--credential-file") {
			t.Fatal("manual guide did not use the fixed release/archive/credential-file")
		}
	}
	if nodeInstallPowerShellQuote("a'b") != "'a''b'" {
		t.Fatal("PowerShell single-quote escaping is not literal")
	}
}

func TestNodeInstallationManualUnixChecksumVariantsExecute(t *testing.T) {
	if runtime.GOOS == "windows" {
		return // Windows syntax is parsed by its native test above.
	}
	for _, platform := range []string{"linux", "darwin"} {
		tool := "sha256sum"
		if platform == "darwin" {
			tool = "shasum"
		}
		if _, err := exec.LookPath(tool); err != nil {
			continue // Each native CI platform exercises its available SHA tool.
		}
		request := nodeInstallationFilesRequest{Method: "manual", Version: "v0.0.1-beta3", OS: platform, Arch: "amd64"}
		asset := "passwall-node_v0.0.1-beta3_" + platform + "_amd64.tar.gz"
		content := []byte("fixture archive bytes, never a downloaded program")
		digest := fmt.Sprintf("%x", sha256.Sum256(content))
		download := manualUnixSteps(nativeServerCreateResponse{}, request)[1].Commands[0]
		// Execute the actual generated checksum selection/check commands only.
		// The preceding production curl commands must never run in this test.
		start := strings.Index(download, "awk -v name=")
		if start < 0 {
			t.Fatal("generated checksum command missing")
		}
		for _, test := range []struct {
			name, sums string
			valid      bool
		}{
			{"exact", digest + "  " + asset + "\n", true},
			{"star", digest + " *" + asset + "\n", true},
			{"uppercase", strings.ToUpper(digest) + "  " + asset + "\n", true},
			{"duplicate", strings.Repeat(digest+"  "+asset+"\n", 2), false},
			{"missing", digest + "  other.tar.gz\n", false},
			{"extra_field", digest + "  " + asset + " extra\n", false},
			{"invalid_hash", strings.Repeat("g", 64) + "  " + asset + "\n", false},
			{"wrong_hash", strings.Repeat("0", 64) + "  " + asset + "\n", false},
		} {
			t.Run(platform+"/"+test.name, func(t *testing.T) {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, asset), content, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS.txt"), []byte(test.sums), 0600); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command("sh", "-c", "set -eu\n"+download[start:])
				cmd.Dir = dir
				output, err := cmd.CombinedOutput()
				if (err == nil) != test.valid {
					t.Fatalf("generated checksum validation: valid=%t, error=%v, output=%s", test.valid, err, output)
				}
			})
		}
	}
}

func TestNodeInstallationManualUnixExtractsOnlyFreshRegularMembers(t *testing.T) {
	if runtime.GOOS == "windows" {
		return
	}
	request := nodeInstallationFilesRequest{Method: "manual", Version: "v0.0.1-beta3", OS: "linux", Arch: "amd64"}
	packageName := "passwall-node_v0.0.1-beta3_linux_amd64"
	for _, test := range []struct {
		name        string
		kind        byte
		duplicate   bool
		preexisting bool
		valid       bool
	}{
		{name: "regular", kind: tar.TypeReg, valid: true},
		{name: "duplicate", kind: tar.TypeReg, duplicate: true},
		{name: "symlink", kind: tar.TypeSymlink},
		{name: "hardlink", kind: tar.TypeLink},
		{name: "preexisting", kind: tar.TypeReg, preexisting: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			var buffer bytes.Buffer
			compressed := gzip.NewWriter(&buffer)
			archive := tar.NewWriter(compressed)
			for _, member := range []string{"passwall-node", "LICENSE", "NOTICE", "../outside"} {
				data := []byte("fixture-" + member)
				kind, size := byte(tar.TypeReg), int64(len(data))
				if member == "passwall-node" {
					kind = test.kind
					if kind != tar.TypeReg {
						size, data = 0, nil
					}
				}
				copies := 1
				if member == "passwall-node" && test.duplicate {
					copies = 2
				}
				for i := 0; i < copies; i++ {
					if err := archive.WriteHeader(&tar.Header{Name: packageName + "/" + member, Mode: 0644, Size: size, Typeflag: kind, Linkname: "../outside"}); err != nil {
						t.Fatal(err)
					}
					if _, err := archive.Write(data); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := archive.Close(); err != nil {
				t.Fatal(err)
			}
			if err := compressed.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, packageName+".tar.gz"), buffer.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			if test.preexisting {
				if err := os.WriteFile(filepath.Join(dir, "passwall-node"), []byte("existing program"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("sh", "-c", manualUnixSteps(nativeServerCreateResponse{}, request)[2].Commands[0])
			cmd.Dir = dir
			output, err := cmd.CombinedOutput()
			if (err == nil) != test.valid {
				t.Fatalf("generated regular-member extraction: valid=%t, error=%v, output=%s", test.valid, err, output)
			}
			if test.valid {
				data, err := os.ReadFile(filepath.Join(dir, "passwall-node"))
				if err != nil || string(data) != "fixture-passwall-node" {
					t.Fatal("the regular binary bytes were not preserved")
				}
			}
			if test.preexisting {
				data, _ := os.ReadFile(filepath.Join(dir, "passwall-node"))
				if string(data) != "existing program" {
					t.Fatal("manual extraction overwrote an existing program")
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "outside")); !os.IsNotExist(err) {
				t.Fatal("manual extraction materialized an unrelated archive path")
			}
		})
	}
}
