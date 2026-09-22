package handler

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

type nodeInstallationFilesRequest struct {
	Version             string `json:"version"`
	Method              string `json:"method"`
	OS                  string `json:"os"`
	Arch                string `json:"arch"`
	DockerRemoteUpgrade bool   `json:"docker_remote_upgrade"`
	// Tag is the ADDRESS of the selected release, and it is UNBINDABLE: the panel
	// fills it from its own catalog after the request is read. A release's address
	// is no longer derivable from its version — four were published under a
	// namespace the ones after them do not use — so the panel states it, and a
	// client-supplied value is not read at all rather than validated.
	Tag string `json:"-"`
}

func (r *nodeInstallationFilesRequest) normalize() bool {
	if (r.Method != "docker" && r.Method != "manual") ||
		(!version.IsReleaseVersion(r.Version) && !(r.Method == "docker" && (r.Version == "latest" || r.Version == "beta"))) {
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
	Name        string `json:"name"`
	Destination string `json:"destination,omitempty"`
	Content     string `json:"content"`
	Sensitive   bool   `json:"sensitive,omitempty"`
}

type nodeInstallationStep struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Commands    []string `json:"commands,omitempty"`
}

type nodeInstallationDownload struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type nodeInstallationFilesResponse struct {
	Method    string                     `json:"method"`
	OS        string                     `json:"os"`
	Arch      string                     `json:"arch,omitempty"`
	Files     []nodeInstallationFile     `json:"files"`
	Downloads []nodeInstallationDownload `json:"downloads,omitempty"`
	Steps     []nodeInstallationStep     `json:"steps"`
}

func renderNodeInstallationFiles(panelID int64, p nativeServerCreateResponse, r nodeInstallationFilesRequest) nodeInstallationFilesResponse {
	result := nodeInstallationFilesResponse{Method: r.Method, OS: r.OS, Arch: r.Arch}
	credential := nodeInstallationFile{Name: "node-credential.txt", Content: p.Credential + "\n", Sensitive: true}
	if r.Method == "docker" {
		credential.Destination = "config/node-credential.txt"
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
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    network_mode: host
    environment:
      PSP_NODE_ENDPOINT: %s
      PSP_NODE_AGENT_ID: %s
      PSP_NODE_ALLOW_INSECURE_HTTP: "false"
      PSP_NODE_DOCKER_REMOTE_UPGRADE: %q
      PSP_NODE_CREDENTIAL_FILE: /run/secrets/passwall-node/node-credential.txt
    volumes:
      - ./config:/run/secrets/passwall-node:ro
      - ./data:/var/lib/passwall-node
    # THE SECURITY PROFILE IS NOT OPTIONAL, AND NOT ONLY FOR SECURITY. The Node
    # project's Docker updater refuses to replace a container whose profile differs
    # from the supported one — it checks that the container is not privileged, has a
    # read-only root filesystem and uses host networking, and it does that BEFORE it
    # pulls anything. A generated file without this line produces a node that can be
    # upgraded in the panel and refused on the host, with a message about the profile
    # that says nothing about the compose it came from.
    read_only: true
    tmpfs:
      - /run/passwall-node:size=64k,mode=0700
      - /tmp:size=16m,mode=1777
    cap_drop:
      - ALL
    # Entrypoint-only capabilities: copy/chown the secret and state volume,
    # protect the runtime tmpfs, then switch to the configured unprivileged
    # UID/GID before exec.
    #
    # FOWNER IS NOT DECORATION. The runtime directory is a tmpfs mount, so it is
    # root-owned; the entrypoint chowns it to the service account and then chmods
    # it, and a root process that is NOT the owner needs CAP_FOWNER for that
    # chmod. With every capability dropped it does not have one: the container
    # starts, prints "chmod: /run/passwall-node: Operation not permitted", reports
    # that it cannot protect its runtime directory, and restarts forever. CHOWN is
    # for the chown, and SETGID/SETUID are for the privilege drop.
    cap_add:
      - CHOWN
      - FOWNER
      - SETGID
      - SETUID
    security_opt:
      - no-new-privileges:true
    stop_grace_period: 30s
`, agentContainer, r.Version, platformLine, composeYAMLString(p.Endpoint), composeYAMLString(p.AgentID), fmt.Sprint(r.DockerRemoteUpgrade))
		if r.DockerRemoteUpgrade {
			updaterContainer := fmt.Sprintf("passwall-node-server-%d-updater", panelID)
			compose = strings.Replace(compose, "    network_mode: host\n", "    depends_on:\n      - passwall-node-updater\n    network_mode: host\n", 1)
			compose = strings.Replace(compose, "      - ./data:/var/lib/passwall-node\n", "      - ./data:/var/lib/passwall-node\n      - ./upgrades:/run/passwall-node-upgrades\n", 1)
			compose += fmt.Sprintf(`    labels:
      io.kazuhahub.passwall-node.managed: "true"
      io.kazuhahub.passwall-node.role: agent
      io.kazuhahub.passwall-node.agent-id: %s

  passwall-node-updater:
    container_name: %s
    image: ghcr.io/kazuhahub/passwall-node:%s
%s    command: ["--run-docker-upgrade-helper"]
    restart: unless-stopped
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
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
      - ./upgrades:/run/passwall-node-upgrades
    stop_grace_period: 15s
`, composeYAMLString(p.AgentID), updaterContainer, r.Version, platformLine, agentContainer, composeYAMLString(p.AgentID))
		}
		result.Files = []nodeInstallationFile{
			{Name: "compose.yaml", Destination: "compose.yaml", Content: compose},
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
		prepareCommand := "set -eu\numask 077\nmkdir -p ./passwall-node-install/config ./passwall-node-install/data\nchmod 0700 ./passwall-node-install ./passwall-node-install/config ./passwall-node-install/data\ncd ./passwall-node-install"
		if r.DockerRemoteUpgrade {
			prepareCommand = "set -eu\numask 077\nmkdir -p ./passwall-node-install/config ./passwall-node-install/data ./passwall-node-install/upgrades\nchmod 0700 ./passwall-node-install ./passwall-node-install/config ./passwall-node-install/data ./passwall-node-install/upgrades\ncd ./passwall-node-install"
		}
		result.Steps = []nodeInstallationStep{
			{ID: "prepare", Title: "Prepare the project directories", Description: "Use Linux Docker Engine with a standard Compose implementation. Unless an architecture was explicitly selected, the multi-platform image selects the host architecture. Use this directory as the NAS project path. Save compose.yaml in its root, save node-credential.txt under config, and preserve config and data together when reinstalling or updating. Stop the old machine before reusing this identity.", Commands: []string{prepareCommand}},
			{ID: "credential", Title: "Place and protect the credential file", Description: "Move the downloaded node-credential.txt into ./config before Compose starts. Compose mounts the complete config directory read-only and the complete data directory read-write, so Docker never interprets a missing credential filename as a directory bind source. Never put the credential in command arguments, environment variables, tracing, shell history or shared logs.", Commands: []string{"set -eu\n[ -d ./config ] && [ ! -L ./config ] || { printf './config must be a real directory\\n' >&2; exit 1; }\n[ -d ./data ] && [ ! -L ./data ] || { printf './data must be a real directory\\n' >&2; exit 1; }\n[ -f ./config/node-credential.txt ] && [ ! -L ./config/node-credential.txt ] || { printf './config/node-credential.txt must be a regular non-symlink file before Docker starts\\n' >&2; exit 1; }\nchmod 0600 ./config/node-credential.txt ./compose.yaml\ndocker compose version\ndocker compose -f compose.yaml config --quiet"}},
			{ID: "start", Title: "Pull and start the selected container image", Description: "The default latest/beta tag follows the selected release channel; an exact version stays pinned for rollback. Start " + serviceSummary + ". Host networking is required by the Agent for PSP-managed dynamic listeners. Choose free listener ports >=1024 unless the Linux host explicitly permits non-root low ports. Do not use a privileged container or mount the Docker socket into the Agent.", Commands: []string{startCommand}},
			{ID: "check", Title: "Verify the version and connect to PSP", Description: "Check the generated service or services, the reported version and Agent status in PSP, then configure nodes separately. Agent heartbeat alone is not proof of a running proxy core. " + upgradeSummary + " Back up the complete private project directory, especially config and data, before updating or reinstalling.", Commands: []string{checkCommand}},
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
	result.Downloads = manualReleaseDownloads(r)
	if r.OS == "windows" {
		result.Steps = manualWindowsSteps(p, r)
	} else {
		result.Steps = manualUnixSteps(p, r)
	}
	return result
}

// nodeReleaseDownloadBase is where a released Passwall Node asset lives.
const nodeReleaseDownloadBase = "https://github.com/KazuhaHub/Passwall-Node/releases/download/"

// releaseTagPath renders a release tag as the path it occupies under
// releases/download/.
//
// THE SLASH IS A SEPARATOR, NOT DATA. GitHub published the tag and serves the
// ref verbatim, so `release/4.0.0` is two path entries and that is the URL that
// exists. Escaping the whole tag asks for a single entry literally named
// `release%2F4.0.0`, which is a different resource — and the earlier version of
// this function did exactly that, on the assumption that it would be equivalent.
// Escaping PER SEGMENT keeps the separator while still neutralising anything
// inside a segment; the tag is validated before it gets here, and this makes the
// URL correct even if a future scheme allows a character that is not.
//
// WHAT A SLASH MEANS HERE IS SETTLED. It was an open question when the namespace
// was chosen — "whether GitHub resolves a slash-bearing tag this way is a question
// to settle with a real download rather than assume" — and it has been settled by
// the four releases published under that namespace, which have been installed from
// these URLs since. The historical namespace is the only slash-bearing one, so this
// path stays for those four and is idle for everything published since.
func releaseTagPath(tag string) string {
	segments := strings.Split(tag, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

// nodeReleaseAssetURL builds the official URL for one asset of one release.
//
// tag IS THE ADDRESS AND asset IS ONE SEGMENT OF IT. The asset name is built
// from request values, so it is escaped whole: a name containing a separator must
// never be read as more path than it is. The tag is the publisher's own ref, so
// its separators are real.
func nodeReleaseAssetURL(tag, asset string) string {
	return nodeReleaseDownloadBase + releaseTagPath(tag) + "/" + url.PathEscape(asset)
}

func manualReleaseDownloads(r nodeInstallationFilesRequest) []nodeInstallationDownload {
	ext := ".tar.gz"
	if r.OS == "windows" {
		ext = ".zip"
	}
	asset := "passwall-node_" + r.Version + "_" + r.OS + "_" + r.Arch + ext
	// THE PATH IS ADDRESSED BY THE TAG AND THE ASSET IS NAMED BY THE VERSION.
	// Passing the version as the path — which this did — asks for a release that
	// does not exist under that name, and the download fails as though the
	// release were missing rather than the address wrong.
	//
	// THE TAG IS THE ONE THE PANEL STATES FOR THIS RELEASE. r.Tag is filled by the
	// handler from the release catalog, because a version no longer determines an
	// address: the four releases published before the namespace changed are not
	// addressed the way deriving one would name them. The derivation is the
	// fallback for a version the catalog does not list — one this panel has never
	// seen published — and it answers in the current namespace, which is what such
	// a version would be published under.
	tag := r.Tag
	if named, ok := version.VersionOfReleaseTag(tag); !ok || named != r.Version {
		derived, ok := version.ReleaseTagFor(r.Version)
		if !ok {
			// Unreachable behind normalize(), which requires a release version. An
			// address that cannot be built is no downloads rather than a guessed one.
			return nil
		}
		tag = derived
	}
	return []nodeInstallationDownload{
		{Name: asset, URL: nodeReleaseAssetURL(tag, asset)},
		{Name: "SHA256SUMS.txt", URL: nodeReleaseAssetURL(tag, "SHA256SUMS.txt")},
	}
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
	check := "sha256sum --check --status selected.sha256"
	if r.OS == "darwin" {
		check = "shasum -a 256 --check selected.sha256"
	}
	verify := fmt.Sprintf(`set -eu
umask 077
[ -f SHA256SUMS.txt ] && [ ! -L SHA256SUMS.txt ] || { printf 'Transfer SHA256SUMS.txt into this directory first\n' >&2; exit 1; }
[ -f %s ] && [ ! -L %s ] || { printf 'Transfer the selected release archive into this directory first\n' >&2; exit 1; }
awk -v name=%s '$2 == name || $2 == "*" name { count++; sum=$1; if (NF != 2) bad=1 } END { if (bad || count != 1 || length(sum) != 64 || sum ~ /[^0-9a-fA-F]/) exit 1; print sum "  " name }' SHA256SUMS.txt > selected.sha256
%s`, nodeInstallShellQuote(asset), nodeInstallShellQuote(asset), nodeInstallShellQuote(asset), check)
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
./passwall-node --endpoint %s --agent-id %s --credential-file "$(pwd -P)/node-credential.txt" --data-dir "$(pwd -P)/data"`, nodeInstallShellQuote(r.Version), nodeInstallShellQuote(p.Endpoint), nodeInstallShellQuote(p.AgentID))
	return []nodeInstallationStep{
		{ID: "prepare", Title: "Prepare a private installation directory", Description: "On a connected administrator device, download the two exact release files listed by PSP and transfer them together with node-credential.txt and node-config.json through a trusted channel. On the target, use a non-root account with tar, awk and the platform SHA-256 tool. Create a NEW directory and keep every file under its exact name. The target host does not need GitHub access.", Commands: []string{"set -eu\numask 077\nmkdir ./passwall-node-manual\nchmod 0700 ./passwall-node-manual\ncd ./passwall-node-manual"}},
		{ID: "download_verify", Title: "Verify the transferred release files", Description: "Require one exact checksum entry for the selected archive. SHA-256 verifies corruption against the same trusted release publisher, not an independent signature. This command performs no network access; stop if it fails.", Commands: []string{verify}},
		{ID: "extract", Title: "Read only the required regular release members", Description: "Never extract arbitrary archive paths, links or ownership into system directories. This reads only the exact binary, LICENSE and NOTICE into the new private directory.", Commands: []string{extract}},
		{ID: "credential", Title: "Protect the credential and state", Description: "node-config.json records the non-secret installation values; it is not a daemon configuration-file flag. Never paste the credential into commands or logs. The data directory retains identity, SQLite counters, downloaded cores and confirmed runtime state. Back up matching credential/config and data before manual updates.", Commands: []string{"set -eu\nchmod 0600 ./node-credential.txt ./node-config.json\nmkdir ./data\nchmod 0700 ./data"}},
		{ID: "run", Title: "Verify the binary version and run in the foreground", Description: "Keep this terminal open. Configure PSP nodes separately after the Agent connects. Use free listener ports >=1024 for this unprivileged mode. This manual mode does not install systemd or the remote upgrade helper; for long-running Linux service use the separate recommended Linux/systemd installer. Stop the old machine before reusing the same credential.", Commands: []string{run}},
	}
}

func manualWindowsSteps(_ nativeServerCreateResponse, r nodeInstallationFilesRequest) []nodeInstallationStep {
	packageName := "passwall-node_" + r.Version + "_windows_" + r.Arch
	asset := packageName + ".zip"
	verify := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$asset = %s
$required = @('SHA256SUMS.txt', $asset)
foreach ($name in $required) {
  if (-not [IO.File]::Exists((Join-Path (Get-Location).Path $name))) { throw ('Transfer the required release file first: ' + $name) }
  $item = Get-Item -Force -LiteralPath $name
  if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw ('Release input is not a regular file: ' + $name) }
}
$entries = [Collections.Generic.List[string[]]]::new()
foreach ($line in [IO.File]::ReadAllLines((Join-Path (Get-Location).Path 'SHA256SUMS.txt'))) {
  $parts = $line.Trim() -split '\s+'
  if ($parts.Length -ge 2 -and ($parts[1] -ceq $asset -or $parts[1] -ceq ('*' + $asset))) { $entries.Add([string[]]$parts) }
}
if ($entries.Count -ne 1 -or $entries[0].Length -ne 2 -or $entries[0][0] -cnotmatch '^[0-9a-fA-F]{64}$') { throw 'Missing, duplicate or invalid checksum entry' }
if ((Get-FileHash -Algorithm SHA256 -LiteralPath $asset).Hash -ine $entries[0][0]) { throw 'Release checksum mismatch' }`, nodeInstallPowerShellQuote(asset))
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
		{ID: "prepare", Title: "Prepare a private installation directory", Description: "On a connected administrator device, download the two exact release files listed by PSP and transfer them together with the generated files through a trusted channel. On the target, use a standard non-administrator account, NTFS and Windows PowerShell >=5.1 with .NET Framework >=4.7.2, or modern PowerShell 7. The target host does not need GitHub access.", Commands: []string{`$ErrorActionPreference = 'Stop'
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
		{ID: "download_verify", Title: "Verify the transferred release files", Description: "Require one exact SHA-256 entry for the selected archive and stop on any failure. This command performs no network access. Checksums trust the same release publisher, not an independent signature.", Commands: []string{verify}},
		{ID: "extract", Title: "Read only the required regular release members", Description: "This reads only the exact binary, LICENSE and NOTICE; it does not unpack arbitrary paths or links and refuses existing destination files.", Commands: []string{extract}},
		{ID: "credential", Title: "Protect the credential and state", Description: "Never put the credential in command arguments, environment variables, tracing or shared logs. Keep node-config.json, node-credential.txt and the persistent data directory together in private backups before manual updates.", Commands: []string{`$ErrorActionPreference = 'Stop'
$owner = [Security.Principal.WindowsIdentity]::GetCurrent().User
foreach ($file in @('node-credential.txt', 'node-config.json')) {
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
$credentialPath = (Resolve-Path -LiteralPath '.\node-credential.txt').Path
$dataPath = (Resolve-Path -LiteralPath '.\data').Path
& '.\passwall-node.exe' --endpoint $config.endpoint --agent-id $config.agent_id --credential-file $credentialPath --data-dir $dataPath
if ($LASTEXITCODE -ne 0) { throw 'Node daemon stopped with an error' }`, nodeInstallPowerShellQuote(r.Version))}},
	}
}
