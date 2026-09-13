#!/usr/bin/env bash
# Private, short-lived authorization. Do not share or run with tracing.
set +x
set -euo pipefail
unset ENV BASH_ENV CDPATH
umask 077

fail() { printf '%s\n' "Passwall Node migration: $1" >&2; exit 1; }
say() { printf '%s\n' "Passwall Node migration: $1"; }

say '[1/4] Checking this node host and existing services...'
[[ "$(id -u)" == 0 ]] || fail 'run as root; no services were changed'
[[ "$(uname -s)" == Linux ]] || fail 'only Linux/systemd is supported; use the manual installation instructions'
case "$(uname -m)" in
    x86_64|amd64|aarch64|arm64) ;;
    *) fail 'only amd64 and arm64 are supported by the reviewed Node installer; no services were changed or PSP conversion requested' ;;
esac
for tool in curl systemctl systemd-detect-virt stat readlink find tar timeout sleep mktemp install chmod cp rm rmdir flock bash sha256sum awk getent useradd chown cmp mv mkdir; do
    command -v "$tool" >/dev/null 2>&1 || fail "required tool missing: $tool; install it and rerun before any service is changed"
done
[[ -d /run/systemd/system && ! -L /run/systemd/system ]] || fail 'systemd is not running; container/unknown deployments require manual shutdown and installation'
if systemd-detect-virt --container --quiet; then
    fail 'container deployments require manual shutdown and installation; no services were changed'
else
    [[ $? == 1 ]] || fail 'cannot identify deployment; no services were changed'
fi
[[ ! -e /.dockerenv && ! -e /run/.containerenv ]] || fail 'container deployments require manual shutdown and installation; no services were changed'
[[ ! -e /opt/passwall-node && ! -L /opt/passwall-node ]] || fail 'an existing Passwall Node installation requires its identity-preserving reinstallation instructions, not backend conversion'

safe_path() {
    local path=$1 parent=$1
    while [[ "$parent" != / ]]; do
        [[ ! -L "$parent" ]] || fail 'symlinked installation or backup paths require manual migration; no conversion was requested'
        if [[ -e "$parent" ]]; then
            local metadata owner mode
            metadata=$(stat -c '%u %a' -- "$parent") || fail 'cannot inspect installation ownership'
            read -r owner mode <<< "$metadata"
            [[ "$owner" == 0 && "$mode" =~ ^[0-7]{3,4}$ ]] || fail 'installation paths must be root-owned'
            (( (8#$mode & 0022) == 0 )) || fail 'group/world-writable installation paths require manual migration'
        fi
        parent=${parent%/*}
        [[ -n "$parent" ]] || parent=/
    done
}

no_old_core() {
    # Inspect, never kill, even a recognizable core outside the supported unit.
    # This also detects the usual Docker core executables in the host PID view.
    local process executable name command_name
    for process in /proc/[0-9]*; do
        [[ -d "$process" ]] || continue
        executable=$(readlink -- "$process/exe" 2>/dev/null) || {
            [[ ! -e "$process/exe" ]] && continue
            fail 'cannot inspect a process executable; confirm old cores are stopped manually'
        }
        name=${executable##*/}
        command_name=''
        if [[ -r "$process/comm" ]]; then
            IFS= read -r command_name < "$process/comm" || true
        fi
        case "$name $command_name" in
            x-ui\ *|3x-ui\ *|xray\ *|xray-*|sing-box\ *|sing-box-*|*\ x-ui|*\ 3x-ui|*\ xray|*\ xray-*|*\ sing-box|*\ sing-box-*)
                fail 'an old x-ui/Xray/sing-box process remains; stop its known deployment manually, then rerun (no unknown PID was killed)'
                ;;
        esac
    done
}

load_state=$(systemctl show x-ui.service --property=LoadState --value 2>/dev/null) || fail 'cannot inspect x-ui.service; no services were changed'
# Job reports only systemd work, not an already-launched remote upgrade shell.
# Administrators must also ensure old panel/core upgrades and external restart
# automation are idle before running this private migration command.
job=$(systemctl show x-ui.service --property=Job --value 2>/dev/null) || fail 'cannot inspect pending x-ui service work; no services were changed'
[[ "$job" == '' || "$job" == 0 ]] || fail 'x-ui has a pending systemd job; wait for upgrades/restarts to finish before migration; no services were changed'
has_old=false
case "$load_state" in
    not-found)
        for path in /usr/local/x-ui /etc/x-ui /etc/default/x-ui /etc/systemd/system/x-ui.service /usr/bin/x-ui /usr/local/bin/x-ui; do
            [[ ! -e "$path" && ! -L "$path" ]] || fail 'unknown/partial x-ui installation remains; back it up and stop it manually before rerunning'
        done
        no_old_core
        ;;
    loaded)
        command -v sqlite3 >/dev/null 2>&1 || fail 'sqlite3 is required for a consistent old database backup; install it and rerun before any service is changed'
        fragment=$(systemctl show x-ui.service --property=FragmentPath --value 2>/dev/null) || fail 'cannot inspect x-ui.service'
        [[ "$fragment" == /etc/systemd/system/x-ui.service ]] || fail 'custom x-ui units require manual migration; no services were changed'
        for path in /etc/systemd/system/x-ui.service /usr/local/x-ui /usr/local/x-ui/x-ui /etc/x-ui /etc/x-ui/x-ui.db; do
            safe_path "$path"
        done
        [[ -f "$fragment" && -d /usr/local/x-ui && -x /usr/local/x-ui/x-ui && -f /usr/local/x-ui/x-ui && -d /etc/x-ui && -f /etc/x-ui/x-ui.db ]] || fail 'incomplete standard x-ui installation requires manual migration'
        [[ "$(systemctl show x-ui.service --property=DropInPaths --value 2>/dev/null)" == '' ]] || fail 'x-ui drop-ins require manual migration; no services were changed'
        [[ "$(systemctl show x-ui.service --property=KillMode --value 2>/dev/null)" == control-group ]] || fail 'custom x-ui process shutdown requires manual migration'
        directory=$(systemctl show x-ui.service --property=WorkingDirectory --value 2>/dev/null) || fail 'cannot inspect x-ui working directory'
        [[ "$directory" == /usr/local/x-ui || "$directory" == /usr/local/x-ui/ ]] || fail 'custom x-ui working directory requires manual migration'
        start=$(systemctl show x-ui.service --property=ExecStart --value 2>/dev/null) || fail 'cannot inspect x-ui executable'
        [[ "$start" == '{ path=/usr/local/x-ui/x-ui ; argv[]=/usr/local/x-ui/x-ui ; '* && "$start" != *' } {'* ]] || fail 'custom x-ui executable/arguments require manual migration'
        for property in ExecStartPre ExecStartPost ExecStop ExecStopPost; do
            extra_command=$(systemctl show x-ui.service --property="$property" --value 2>/dev/null) || fail 'cannot inspect x-ui service hooks'
            [[ "$extra_command" == '' ]] || fail 'custom x-ui service hooks require manual migration; no services were changed'
        done
        environment_files=$(systemctl show x-ui.service --property=EnvironmentFiles --value 2>/dev/null) || fail 'cannot inspect x-ui environment files'
        [[ "$environment_files" == '' || "$environment_files" == '/etc/default/x-ui (ignore_errors=yes)' ]] || fail 'custom x-ui environment files require manual migration'
        [[ ! -e /etc/default/x-ui && ! -L /etc/default/x-ui ]] || fail 'x-ui environment/database overrides require manual backup and migration'
        environment=$(systemctl show x-ui.service --property=Environment --value 2>/dev/null) || fail 'cannot inspect x-ui environment'
        [[ "$environment" == '' || "$environment" == XRAY_VMESS_AEAD_FORCED=false ]] || fail 'custom x-ui environment/database paths require manual migration'
        # Matching ExecStart/WorkingDirectory strings do not prove that the
        # service sees the host's files. A chroot/image, bind mount or joined
        # namespace could redirect the same /etc/x-ui path to another database,
        # making a successful host SQLite backup the wrong backup. Refuse such
        # deployments rather than stopping a service whose data we cannot back up.
        for property in RootDirectory RootImage BindPaths BindReadOnlyPaths TemporaryFileSystem MountImages ExtensionImages ExtensionDirectories JoinsNamespaceOf; do
            mapping=$(systemctl show x-ui.service --property="$property" --value 2>/dev/null) || fail 'cannot inspect x-ui filesystem namespace; no services were changed'
            [[ "$mapping" == '' ]] || fail 'custom x-ui filesystem namespaces require manual backup and migration; no services were changed'
        done
        has_old=true
        ;;
    *) fail 'unknown x-ui deployment requires manual shutdown and installation; no services were changed' ;;
esac

safe_path /var
[[ -d /var ]] || fail 'the standard /var directory is required'
safe_path /var/backups
if [[ ! -e /var/backups ]]; then
    install -d -m 0755 -- /var/backups || fail 'cannot create the backup parent directory'
fi
[[ -d /var/backups ]] || fail 'the backup parent must be a directory'
backup_parent=/var/backups/passwall-node-migration
safe_path "$backup_parent"
if [[ -e "$backup_parent" ]]; then
    [[ -d "$backup_parent" && "$(stat -c '%a' -- "$backup_parent")" == 700 ]] || fail 'the existing migration backup directory must be private/root-owned'
else
    install -d -m 0700 -- "$backup_parent" || fail 'cannot create the private backup directory'
fi
safe_path "$backup_parent/.bootstrap.lock"
[[ ! -e "$backup_parent/.bootstrap.lock" || -f "$backup_parent/.bootstrap.lock" ]] || fail 'unsafe migration lock'
exec 9> "$backup_parent/.bootstrap.lock"
flock -n 9 || fail 'another migration is running on this node'
work=$(mktemp -d "$backup_parent/.bootstrap.XXXXXXXXXX") || fail 'cannot create a private temporary directory'
cleanup() {
    # Only these two files and the freshly-created private directory are removed.
    rm -f -- "$work/authorization" "$work/install.sh" "$work/config-paths"
    rmdir -- "$work" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

if [[ "$has_old" == true ]]; then
    say '[2/4] Backing up the old configuration, then stopping x-ui...'
    # A checked enumeration avoids silently ignoring errors from a process
    # substitution. Never dereference links or copy special configuration files.
    find -P /etc/x-ui -xdev -print0 > "$work/config-paths" || fail 'cannot enumerate old configuration; no services were changed'
    count=0
    while IFS= read -r -d '' path; do
        count=$((count + 1))
        (( count <= 10000 )) || fail 'x-ui configuration tree is too large for automatic migration'
        safe_path "$path"
        [[ -d "$path" || -f "$path" ]] || fail 'special x-ui configuration files require manual backup'
    done < "$work/config-paths"
    backup=$(mktemp -d "$backup_parent/backup.XXXXXXXXXX") || fail 'cannot create the private x-ui backup directory'
    cp -- /etc/systemd/system/x-ui.service "$backup/x-ui.service" || fail 'cannot back up the old unit; no services were changed'
    timeout 120 sqlite3 /etc/x-ui/x-ui.db ".backup '$backup/x-ui.db'" >/dev/null 2>&1 || fail 'cannot make a consistent SQLite backup; no services were changed'
    [[ "$(timeout 30 sqlite3 "$backup/x-ui.db" 'PRAGMA quick_check;' 2>/dev/null)" == ok ]] || fail 'the SQLite backup failed verification; no services were changed'
    tar -C /etc/x-ui --exclude=./x-ui.db --exclude=./x-ui.db-wal --exclude=./x-ui.db-shm -cf "$backup/etc-x-ui.tar" . || fail 'cannot back up old configuration; no services were changed'
    chmod 0600 -- "$backup/x-ui.service" "$backup/x-ui.db" "$backup/etc-x-ui.tar"
    say "old x-ui configuration/database/unit backed up under $backup; the old installation will not be removed"
    timeout 90 systemctl stop x-ui.service || fail "old service shutdown failed; backup retained under $backup; no PSP conversion was requested"
    timeout 30 systemctl disable x-ui.service || fail "old service disable failed; backup retained under $backup; no PSP conversion was requested"
    [[ "$(systemctl show x-ui.service --property=MainPID --value 2>/dev/null)" == 0 && "$(systemctl show x-ui.service --property=ControlPID --value 2>/dev/null)" == 0 ]] || fail 'old service processes remain; no PSP conversion was requested'
    [[ "$(systemctl show x-ui.service --property=ActiveState --value 2>/dev/null)" == inactive ]] || fail 'old service is not inactive; no PSP conversion was requested'
    no_old_core
    # Capture the final quiescent state as well, including changes between the
    # initial live backup and graceful shutdown. No automatic restore is done.
    timeout 120 sqlite3 /etc/x-ui/x-ui.db ".backup '$backup/final-x-ui.db'" >/dev/null 2>&1 || fail "final SQLite backup failed; old service stays stopped and initial backup remains under $backup"
    [[ "$(timeout 30 sqlite3 "$backup/final-x-ui.db" 'PRAGMA quick_check;' 2>/dev/null)" == ok ]] || fail 'final SQLite backup verification failed; no PSP conversion was requested'
    tar -C /etc/x-ui --exclude=./x-ui.db --exclude=./x-ui.db-wal --exclude=./x-ui.db-shm -cf "$backup/final-etc-x-ui.tar" . || fail 'final configuration backup failed; no PSP conversion was requested'
    chmod 0600 -- "$backup/final-x-ui.db" "$backup/final-etc-x-ui.tar"
else
    say '[2/4] Clean system: no old installation needs backup or shutdown.'
    say 'clean systemd node confirmed; there is no old x-ui service/configuration to stop or remove'
fi

# printf is a shell builtin. The ticket is neither a process argument to curl
# nor an exported environment variable, and redirects are never followed.
printf 'Authorization: Bearer %s\n' @@TICKET@@ > "$work/authorization"
chmod 0600 -- "$work/authorization"
: > "$work/install.sh"
chmod 0600 -- "$work/install.sh"
say '[3/4] Asking PSP to convert this same server; this can take up to 90 seconds...'
http_result=$(curl --disable --silent --show-error --proto '=https' --proto-redir '=https' --connect-timeout 10 --max-time 90 --max-filesize 1048576 \
    --request POST --header "@$work/authorization" --header 'Content-Type: application/json' \
    --data '{"old_backend_stopped":true}' --output "$work/install.sh" --write-out '%{http_code} %{content_type}' \
    @@COMPLETION_URL@@ 2>/dev/null) || fail 'PSP completion request failed; old service stays stopped, backups retained; rerun only using the PSP recovery instructions'
status=${http_result%% *}
content_type=${http_result#* }
[[ "$status" =~ ^2[0-9][0-9]$ && ( "$content_type" == text/plain || "$content_type" == text/plain\;* ) ]] || fail 'PSP did not return a successful private installation script; old service stays stopped and backups are retained'
[[ -s "$work/install.sh" && "$(stat -c '%s' -- "$work/install.sh")" -le 1048576 ]] || fail 'PSP returned an empty/oversized installation script; use the PSP recovery instructions'
bash -n "$work/install.sh" 2>/dev/null || fail 'PSP returned an invalid installation script; use the PSP recovery instructions'
rm -f -- "$work/authorization"
say 'PSP accepted conversion; executing its exact-version private Passwall Node installer'
say '[4/4] Installing Passwall Node. Follow the installer progress below...'
bash "$work/install.sh" || fail 'installation failed after PSP conversion; keep backups and use PSP installation instructions for the same server identity (do not re-enable old x-ui automatically)'
say 'Installation completed. Return to PSP and check this server is online, its nodes/core are applied, then test an actual proxy connection.'
if [[ "$has_old" == true ]]; then
    say "Old x-ui remains installed and disabled; backups are retained under $backup."
fi
