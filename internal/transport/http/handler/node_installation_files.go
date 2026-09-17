package handler

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/KazuhaHub/passwall-node/deployment"
)

type nodeInstallationFilesRequest struct {
	Version             string `json:"version"`
	Method              string `json:"method"`
	OS                  string `json:"os"`
	Arch                string `json:"arch"`
	DockerRemoteUpgrade bool   `json:"docker_remote_upgrade"`
}

func (r *nodeInstallationFilesRequest) normalize() bool {
	if (r.Method != "docker" && r.Method != "manual") ||
		(!deployment.ValidReleaseVersion(r.Version) && !(r.Method == "docker" && (r.Version == "latest" || r.Version == "beta"))) {
		return false
	}
	if r.OS == "" {
		r.OS = "linux"
	}
	if r.Arch == "" && r.Method == "manual" {
		r.Arch = "amd64"
	}
	if r.Arch != "amd64" && r.Arch != "arm64" && !(r.Method == "docker" && r.Arch == "") {
		return false
	}
	if r.Method != "docker" {
		r.DockerRemoteUpgrade = false
	}
	return r.OS == "linux" || (r.Method == "manual" && (r.OS == "darwin" || r.OS == "windows"))
}

type nodeInstallationFile struct {
	Name      string `json:"name"`
	Content   string `json:"content"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

type nodeInstallationStep struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Commands    []string `json:"commands,omitempty"`
}

type nodeInstallationFilesResponse struct {
	Method string                 `json:"method"`
	OS     string                 `json:"os"`
	Arch   string                 `json:"arch,omitempty"`
	Files  []nodeInstallationFile `json:"files"`
	Steps  []nodeInstallationStep `json:"steps"`
}

func renderNodeInstallationFiles(panelID int64, p nativeServerCreateResponse, r nodeInstallationFilesRequest) nodeInstallationFilesResponse {
	result := nodeInstallationFilesResponse{Method: r.Method, OS: r.OS, Arch: r.Arch}
	credentialName := "node-credential"
	if r.Method == "docker" {
		// NAS file managers may treat the extensionless secret source as a folder
		// during import. Keep the manual-install contract unchanged, but make the
		// Docker artifact unambiguously a regular text file.
		credentialName = "node-credential.txt"
	}
	credential := nodeInstallationFile{Name: credentialName, Content: p.Credential + "\n", Sensitive: true}
	if r.Method == "docker" {
		// Keep the default file within the conservative Compose subset understood by
		// NAS project editors as well as Docker Compose itself. Endpoint and agent ID
		// are not secrets; the long-lived credential remains a read-only host file.
		// Compose treats $$ as a literal $, so quote after escaping interpolation.
		platformLine := ""
		if r.Arch != "" {
			platformLine = "    platform: linux/" + r.Arch + "\n"
		}
		agentContainer := fmt.Sprintf("passwall-node-server-%d-agent", panelID)
		compose := fmt.Sprintf(`services:
  passwall-node:
    container_name: %s
    image: ghcr.io/kazuhahub/passwall-node:%s
%s    restart: unless-stopped
    network_mode: host
    environment:
      PSP_NODE_ENDPOINT: %s
      PSP_NODE_AGENT_ID: %s
      PSP_NODE_ALLOW_INSECURE_HTTP: "false"
      PSP_NODE_DOCKER_REMOTE_UPGRADE: %q
    volumes:
      - ./node-credential.txt:/run/secrets/node_credential:ro
      - passwall-node-data:/var/lib/passwall-node
    stop_grace_period: 30s
`, agentContainer, r.Version, platformLine, composeYAMLString(p.Endpoint), composeYAMLString(p.AgentID), fmt.Sprint(r.DockerRemoteUpgrade))
		if r.DockerRemoteUpgrade {
			updaterContainer := fmt.Sprintf("passwall-node-server-%d-updater", panelID)
			compose = strings.Replace(compose, "    network_mode: host\n", "    depends_on:\n      - passwall-node-updater\n    network_mode: host\n", 1)
			compose = strings.Replace(compose, "      - passwall-node-data:/var/lib/passwall-node\n", "      - passwall-node-data:/var/lib/passwall-node\n      - passwall-node-upgrades:/run/passwall-node-upgrades\n", 1)
			compose += fmt.Sprintf(`    labels:
      io.kazuhahub.passwall-node.managed: "true"
      io.kazuhahub.passwall-node.role: agent
      io.kazuhahub.passwall-node.agent-id: %s

  passwall-node-updater:
    container_name: %s
    image: ghcr.io/kazuhahub/passwall-node:%s
%s    command: ["--run-docker-upgrade-helper"]
    restart: unless-stopped
    network_mode: none
    environment:
      PSP_NODE_UPGRADE_TARGET_CONTAINER: %s
      PSP_NODE_UPGRADE_TARGET_AGENT_ID: %s
      PUID: "10001"
      PGID: "10001"
    volumes:
      # Docker socket access is root-equivalent. It belongs only to this
      # isolated updater; never mount it into the network-facing Agent.
      - /var/run/docker.sock:/var/run/docker.sock
      - passwall-node-upgrades:/run/passwall-node-upgrades
    stop_grace_period: 15s
`, composeYAMLString(p.AgentID), updaterContainer, r.Version, platformLine, agentContainer, composeYAMLString(p.AgentID))
		}
		compose += "volumes:\n  passwall-node-data:\n"
		if r.DockerRemoteUpgrade {
			compose += "  passwall-node-upgrades:\n"
		}
		result.Files = []nodeInstallationFile{
			{Name: "compose.yaml", Content: compose},
			credential,
		}
		serviceSummary := "the Agent service"
		startCommand := "set -eu\ndocker compose -f compose.yaml pull passwall-node\ndocker compose -f compose.yaml up -d passwall-node"
		checkCommand := "set -eu\ndocker compose -f compose.yaml exec -T passwall-node /usr/local/bin/passwall-node --version\ndocker compose -f compose.yaml ps passwall-node"
		upgradeSummary := "Docker remote upgrade is disabled by default; enable the advanced option and regenerate if this host may expose its Docker socket to the isolated updater."
		if r.DockerRemoteUpgrade {
			serviceSummary = "the Agent and isolated updater services"
			startCommand = "set -eu\ndocker compose -f compose.yaml pull passwall-node passwall-node-updater\ndocker compose -f compose.yaml up -d"
			checkCommand = "set -eu\ndocker compose -f compose.yaml exec -T passwall-node /usr/local/bin/passwall-node --version\ndocker compose -f compose.yaml ps passwall-node passwall-node-updater"
			upgradeSummary = "The updater enables authenticated remote upgrades with automatic rollback; it accepts official exact releases only and never receives the node credential."
		}
		result.Steps = []nodeInstallationStep{
			{ID: "prepare", Title: "Prepare a private installation directory", Description: "Use Linux Docker Engine with a standard Compose implementation. Unless an architecture was explicitly selected, the multi-platform image selects the host architecture. Save compose.yaml and node-credential.txt with their exact names in the same private project directory before validating or deploying. NAS project editors must use that directory as the project path. Preserve the same project and data volume when reinstalling or updating. Stop the old machine before reusing this identity.", Commands: []string{"set -eu\numask 077\nmkdir ./passwall-node-install\nchmod 0700 ./passwall-node-install\ncd ./passwall-node-install"}},
			{ID: "credential", Title: "Protect the credential file", Description: "Transfer the files through a private channel. Never put the credential in command arguments, environment variables, tracing, shell history or shared logs.", Commands: []string{"set -eu\nchmod 0600 ./node-credential.txt ./compose.yaml\ndocker compose version\ndocker compose -f compose.yaml config --quiet"}},
			{ID: "start", Title: "Pull and start the selected container image", Description: "The default latest/beta tag follows the selected release channel; an exact version stays pinned for rollback. Start " + serviceSummary + ". Host networking is required by the Agent for PSP-managed dynamic listeners. Choose free listener ports >=1024 unless the Linux host explicitly permits non-root low ports. Do not use a privileged container or mount the Docker socket into the Agent.", Commands: []string{startCommand}},
			{ID: "check", Title: "Verify the version and connect to PSP", Description: "Check the generated service or services, the reported version and Agent status in PSP, then configure nodes separately. Agent heartbeat alone is not proof of a running proxy core. " + upgradeSummary + " Back up this private directory and the persistent data volume; never run compose down --volumes to update.", Commands: []string{checkCommand}},
		}
		return result
	}
	// This JSON is an installation record, not a new daemon config-file format.
	// Unix commands use the same generated non-secret values as literal argv;
	// PowerShell reads the record as data rather than dot-sourcing an env file.
	config, _ := json.MarshalIndent(struct {
		Endpoint string `json:"endpoint"`
		AgentID  string `json:"agent_id"`
		Version  string `json:"version"`
		OS       string `json:"os"`
		Arch     string `json:"arch"`
	}{p.Endpoint, p.AgentID, r.Version, r.OS, r.Arch}, "", "  ")
	result.Files = []nodeInstallationFile{{Name: "node-config.json", Content: string(config) + "\n"}, credential}
	if r.OS == "windows" {
		result.Steps = manualWindowsSteps(p, r)
	} else {
		result.Steps = manualUnixSteps(p, r)
	}
	return result
}

func composeYAMLString(value string) string {
	encoded, _ := json.Marshal(strings.ReplaceAll(value, "$", "$$"))
	return string(encoded)
}

func nodeInstallShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
func nodeInstallPowerShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func manualUnixSteps(p nativeServerCreateResponse, r nodeInstallationFilesRequest) []nodeInstallationStep {
	packageName := "passwall-node_" + r.Version + "_" + r.OS + "_" + r.Arch
	asset := packageName + ".tar.gz"
	base := "https://github.com/KazuhaHub/Passwall-Node/releases/download/" + r.Version
	check := "sha256sum --check --status selected.sha256"
	if r.OS == "darwin" {
		check = "shasum -a 256 --check selected.sha256"
	}
	download := fmt.Sprintf(`set -eu
umask 077
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 15 --max-time 120 --output SHA256SUMS.txt %s
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 15 --max-time 300 --output %s %s
awk -v name=%s '$2 == name || $2 == "*" name { count++; sum=$1; if (NF != 2) bad=1 } END { if (bad || count != 1 || length(sum) != 64 || sum ~ /[^0-9a-fA-F]/) exit 1; print sum "  " name }' SHA256SUMS.txt > selected.sha256
%s`, nodeInstallShellQuote(base+"/SHA256SUMS.txt"), nodeInstallShellQuote(asset), nodeInstallShellQuote(base+"/"+asset), nodeInstallShellQuote(asset), check)
	extract := fmt.Sprintf(`set -eu
umask 077
package=%s
asset=%s
for name in passwall-node LICENSE NOTICE; do
  member="$package/$name"
  count=$(tar -tzf "$asset" | awk -v wanted="$member" '$0 == wanted { count++ } END { print count+0 }')
  [ "$count" = 1 ] || { printf 'Missing or duplicate release member\n' >&2; exit 1; }
  kind=$(tar -tvzf "$asset" "$member")
  case "$kind" in -*) ;; *) printf 'Release member is not a regular file\n' >&2; exit 1 ;; esac
  [ ! -e "./$name" ] && [ ! -L "./$name" ] || { printf 'Destination already exists; inspect it manually\n' >&2; exit 1; }
  tar -xOzf "$asset" "$member" > "./$name"
  [ -s "./$name" ] || { printf 'Empty release member\n' >&2; exit 1; }
done
chmod 0755 ./passwall-node`, nodeInstallShellQuote(packageName), nodeInstallShellQuote(asset))
	run := fmt.Sprintf(`set -eu
[ "$(id -u)" -ne 0 ] || { printf 'Run the manual daemon as a non-root user\n' >&2; exit 1; }
reported=$(./passwall-node --version)
[ "${reported%%%% *}" = %s ] || { printf 'Binary release version mismatch\n' >&2; exit 1; }
./passwall-node --endpoint %s --agent-id %s --credential-file "$(pwd -P)/node-credential" --data-dir "$(pwd -P)/data"`, nodeInstallShellQuote(r.Version), nodeInstallShellQuote(p.Endpoint), nodeInstallShellQuote(p.AgentID))
	return []nodeInstallationStep{
		{ID: "prepare", Title: "Prepare a private installation directory", Description: "Use a non-root account on the selected OS/architecture with curl, tar, awk and the platform SHA-256 tool. Create a NEW directory and save node-credential and node-config.json there under their exact names through a private channel. Do not overwrite an existing installation or data directory.", Commands: []string{"set -eu\numask 077\nmkdir ./passwall-node-manual\nchmod 0700 ./passwall-node-manual\ncd ./passwall-node-manual"}},
		{ID: "download_verify", Title: "Download and verify the exact release", Description: "Download only from the official fixed release tag and require one exact checksum entry. SHA-256 verifies corruption against the same trusted HTTPS publisher, not an independent signature. Stop if any command fails.", Commands: []string{download}},
		{ID: "extract", Title: "Read only the required regular release members", Description: "Never extract arbitrary archive paths, links or ownership into system directories. This reads only the exact binary, LICENSE and NOTICE into the new private directory.", Commands: []string{extract}},
		{ID: "credential", Title: "Protect the credential and state", Description: "node-config.json records the non-secret installation values; it is not a daemon configuration-file flag. Never paste the credential into commands or logs. The data directory retains identity, SQLite counters, downloaded cores and confirmed runtime state. Back up matching credential/config and data before manual updates.", Commands: []string{"set -eu\nchmod 0600 ./node-credential ./node-config.json\nmkdir ./data\nchmod 0700 ./data"}},
		{ID: "run", Title: "Verify the binary version and run in the foreground", Description: "Keep this terminal open. Configure PSP nodes separately after the Agent connects. Use free listener ports >=1024 for this unprivileged mode. This manual mode does not install systemd or the remote upgrade helper; for long-running Linux service use the separate recommended Linux/systemd installer. Stop the old machine before reusing the same credential.", Commands: []string{run}},
	}
}

func manualWindowsSteps(_ nativeServerCreateResponse, r nodeInstallationFilesRequest) []nodeInstallationStep {
	packageName := "passwall-node_" + r.Version + "_windows_" + r.Arch
	asset := packageName + ".zip"
	base := "https://github.com/KazuhaHub/Passwall-Node/releases/download/" + r.Version
	download := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
& curl.exe --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 15 --max-time 120 --output SHA256SUMS.txt %s
if ($LASTEXITCODE -ne 0) { throw 'Checksum download failed' }
& curl.exe --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 15 --max-time 300 --output %s %s
if ($LASTEXITCODE -ne 0) { throw 'Release download failed' }
$asset = %s
$entries = [Collections.Generic.List[string[]]]::new()
foreach ($line in [IO.File]::ReadAllLines((Join-Path (Get-Location).Path 'SHA256SUMS.txt'))) {
  $parts = $line.Trim() -split '\s+'
  if ($parts.Length -ge 2 -and ($parts[1] -ceq $asset -or $parts[1] -ceq ('*' + $asset))) { $entries.Add([string[]]$parts) }
}
if ($entries.Count -ne 1 -or $entries[0].Length -ne 2 -or $entries[0][0] -cnotmatch '^[0-9a-fA-F]{64}$') { throw 'Missing, duplicate or invalid checksum entry' }
if ((Get-FileHash -Algorithm SHA256 -LiteralPath $asset).Hash -ine $entries[0][0]) { throw 'Release checksum mismatch' }`, nodeInstallPowerShellQuote(base+"/SHA256SUMS.txt"), nodeInstallPowerShellQuote(asset), nodeInstallPowerShellQuote(base+"/"+asset), nodeInstallPowerShellQuote(asset))
	extract := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.IO.Compression.FileSystem
if ($null -eq [IO.Compression.ZipArchiveEntry].GetProperty('ExternalAttributes')) { throw 'Archive member validation requires .NET Framework 4.7.2 or later, or modern PowerShell 7' }
$archive = [IO.Compression.ZipFile]::OpenRead((Join-Path (Get-Location).Path %s))
try {
  foreach ($name in @('passwall-node.exe', 'LICENSE', 'NOTICE')) {
    $wanted = %s + '/' + $name
    $members = @($archive.Entries | Where-Object { $_.FullName -ceq $wanted })
    if ($members.Count -ne 1) { throw 'Missing or duplicate release member' }
    $entry = $members[0]
    $kind = ($entry.ExternalAttributes -shr 16) -band 0xF000
    if (($kind -ne 0 -and $kind -ne 0x8000) -or ($entry.ExternalAttributes -band 0x10) -ne 0 -or $entry.Length -le 0 -or $entry.FullName.EndsWith('/')) { throw 'Release member is not a nonempty regular file' }
    $source = $entry.Open()
    try {
      $target = [IO.File]::Open((Join-Path (Get-Location).Path $name), [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
      try { $source.CopyTo($target) } finally { $target.Dispose() }
    } finally { $source.Dispose() }
  }
} finally { $archive.Dispose() }`, nodeInstallPowerShellQuote(asset), nodeInstallPowerShellQuote(packageName))
	return []nodeInstallationStep{
		{ID: "prepare", Title: "Prepare a private installation directory", Description: "Use a standard non-administrator account, NTFS, curl.exe and Windows PowerShell >=5.1 with .NET Framework >=4.7.2, or modern PowerShell 7, on the selected Windows architecture. Create a NEW directory, protect its ACL, then save the two generated files there under their exact names through a private channel. Do not target existing state.", Commands: []string{`$ErrorActionPreference = 'Stop'
$directory = Join-Path (Get-Location).Path 'passwall-node-manual'
if (Test-Path -LiteralPath $directory) { throw 'Installation directory already exists; inspect it manually' }
New-Item -ItemType Directory -Path $directory | Out-Null
$owner = [Security.Principal.WindowsIdentity]::GetCurrent().User
$acl = [Security.AccessControl.DirectorySecurity]::new()
$acl.SetOwner($owner)
$acl.SetAccessRuleProtection($true, $false)
$acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($owner, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow'))
Set-Acl -LiteralPath $directory -AclObject $acl
Set-Location -LiteralPath $directory`}},
		{ID: "download_verify", Title: "Download and verify the exact release", Description: "Use the official fixed HTTPS tag only, require one exact SHA-256 entry and stop on any failure. Checksums trust the same release publisher, not an independent signature.", Commands: []string{download}},
		{ID: "extract", Title: "Read only the required regular release members", Description: "This reads only the exact binary, LICENSE and NOTICE; it does not unpack arbitrary paths or links and refuses existing destination files.", Commands: []string{extract}},
		{ID: "credential", Title: "Protect the credential and state", Description: "Never put the credential in command arguments, environment variables, tracing or shared logs. Keep node-config.json, node-credential and the persistent data directory together in private backups before manual updates.", Commands: []string{`$ErrorActionPreference = 'Stop'
$owner = [Security.Principal.WindowsIdentity]::GetCurrent().User
foreach ($file in @('node-credential', 'node-config.json')) {
  $acl = [Security.AccessControl.FileSecurity]::new()
  $acl.SetOwner($owner)
  $acl.SetAccessRuleProtection($true, $false)
  $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($owner, 'FullControl', 'Allow'))
  Set-Acl -LiteralPath $file -AclObject $acl
}
if (Test-Path -LiteralPath '.\data') { throw 'Data directory already exists; inspect it manually' }
New-Item -ItemType Directory -Path '.\data' | Out-Null`}},
		{ID: "run", Title: "Verify the binary version and run in the foreground", Description: "Keep this terminal open and configure PSP nodes separately after the Agent connects. This does not register a Windows service or Linux/systemd remote upgrade helper. Stop the old machine before reusing its credential.", Commands: []string{fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$config = Get-Content -Raw -LiteralPath '.\node-config.json' | ConvertFrom-Json
$reported = & '.\passwall-node.exe' --version
if ($LASTEXITCODE -ne 0 -or (($reported -join ' ') -split '\s+')[0] -cne %s) { throw 'Binary release version mismatch' }
$credentialPath = (Resolve-Path -LiteralPath '.\node-credential').Path
$dataPath = (Resolve-Path -LiteralPath '.\data').Path
& '.\passwall-node.exe' --endpoint $config.endpoint --agent-id $config.agent_id --credential-file $credentialPath --data-dir $dataPath
if ($LASTEXITCODE -ne 0) { throw 'Node daemon stopped with an error' }`, nodeInstallPowerShellQuote(r.Version))}},
	}
}
