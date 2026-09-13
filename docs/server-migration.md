# Migrate an existing 3X-UI server to Passwall Node

The administrator's Servers menu has one **Install / Reinstall** entry with
a backend chooser. An existing record defaults to its current backend; a new
server defaults to Passwall Node. Choosing 3X-UI → Passwall Node opens a read-only
migration preflight; an existing Passwall Node opens the installation methods.
Opening either flow does not stop services or change the server identity.
Ordinary Passwall Node reinstallation does not convert a backend or require
stopping PSP. S-UI conversion is not yet available. Same-backend 3X-UI/S-UI
reinstallation offers official manual instructions and editing the **original**
connection record; it does not claim that updating a Token restores configuration.

## Default: one command on the node host

This online workflow supports **exactly one PSP process using the database**.
Confirm this explicitly; an in-process admission gate is not a distributed lock.
Multiple instances must use the stopped-deployment fallback below.

1. Back up the PSP database and configuration/key material. Review the managed-only
   scope and every preflight warning/blocker. Choose an exact reviewed Node release
   and exact verified Xray core. Opening or generating the command changes no rows.
2. Ensure there are no panel/core upgrade jobs or external automation that could
   restart the old service. Generate the private command and run it as root **on
   the node**, not on the PSP host. The delivery URL expires after 15 minutes and
   downloads once; it contains a temporary authorization, not the fixed credential.
3. The wrapper supports standard, root-owned Linux amd64/arm64 `x-ui.service`
   deployments or a clean systemd host after OS reinstallation. It checks required
   tools/deployment, verifies consistent SQLite/config/unit backups, then stops
   and disables the old service. The old installation and private backups remain.
   Custom units, containers, partial installations, unsafe paths or identifiable
   remaining cores require manual handling; unknown processes are never killed.
4. The node calls PSP with a separate expiring authorization. PSP drains admitted
   HTTP/background operations including their subsequent writes, rechecks the
   fingerprint in its atomic conversion, and replaces the backend adapter before
   readmission. The node receives an exact-version private installer containing
   the credential for the **same** server record.
5. A retry of this callback within its lifetime returns the same identity and
   credential, not another agent. A failed/ambiguous commit or adapter replacement
   fails closed. Inspect the original record, regenerate its installation materials
   if it is already Passwall Node, and never blindly re-enable old Xray. A download
   lost before execution can be regenerated without rotating a native identity.
6. Confirm the actual agent/core, config/client application and a proxy connection.
   A successful installer or heartbeat alone does not establish proxy readiness.

The command is sensitive. Do not share it or run with shell tracing; consider
terminal history and configure reverse-proxy access logs not to retain delivery
tokens. PSP omits these URLs from its own request-path logs and performs mandatory
metadata-only auditing before issuing or delivering a bootstrap script.
Restarting PSP invalidates outstanding delivery commands, not node credentials.
There is no automatic VM/database rollback, old-install uninstall or distributed
recovery workflow.

## What stays the same

The conversion retains the original server, node, inbound, client and attachment
IDs. PSP-managed connection settings, addresses/ports, confirmed user credentials,
group membership, tags, sorting and historical traffic remain in their existing
records. A new Passwall Node agent identity and encrypted fixed installation
credential are attached to that same server. Existing upstream credentials are
cleared; the old 3X-UI installation is not uninstalled.

Only PSP-managed snapshots and clients migrate. The first implementation supports
Xray VLESS, VMess, Trojan and Shadowsocks, with a selectable, handshake-verified
Xray version from the pinned Passwall Node catalog. The current known core is
preferred; choosing the recommended core is explicit, not an automatic downgrade.
Restricted catalog versions require explicit compatibility acknowledgement.
The preview identifies the catalog's REALITY compatibility normalization.

Uncaptured/unconfirmed configuration, credential rotations, cross-server/orphan
attachments, unsupported inbound expiry, external certificate/key files and
detected local/global dependencies block conversion. Protocol/environment
validation is not a substitute for testing the actual installed core.
REALITY with a nonempty `finalmask.tcp` also blocks conversion: the old upstream
push normalizes this field away, whereas Passwall Node would pass it to Xray.
Clean it on the original node, recapture and confirm the configuration before
migrating; the conversion does not silently strip fields. See the upstream
[Xray issue](https://github.com/XTLS/Xray-core/issues/6453) for the reported crash.

Global routing, DNS, outbounds, manually created clients/inbounds and external
files are **not** migrated. Before accepting `--managed-only`, verify that the
PSP-managed nodes do not require these omitted settings or objects. In particular,
stop if they rely on WARP/custom outbounds or an unmigrated local fallback.
Connection/IP/device-limit differences are reported as warnings, not silently
claimed to be equivalent.

S-UI is not accepted by this command. Its current PSP snapshot is a selective
sing-box-to-Xray projection and does not establish whether omitted raw transport
or TLS fields are empty. S-UI migration needs raw inbound/TLS comparison and a
verified actual sing-box version before lossless conversion can be offered.

## Advanced fallback: stopped-deployment CLI

1. Successfully start the current V4 PSP version normally before maintenance.
   Use the preflight to review every blocker/warning and select the exact core.
2. Back up the current database **and configuration/key material**. Keep the old
   node installation/configuration and its certificate files available.
3. Stop old Xray on the node and prevent its supervisor/container from restarting
   it. A disconnected 3X-UI panel does **not** prove Xray stopped.
4. Fully stop **all PSP processes sharing this database**. Confirm they have exited,
   not merely that a shutdown signal was sent. This applies to MySQL/PostgreSQL
   deployments too, not only SQLite. Never execute inside a still-running PSP
   container with `docker exec`.
5. Run a dry preview using the same PSP binary/image, config, database and env.
   Dry run is the default. Copy its fingerprint if the online preview became stale.
6. Run `--apply` with that fingerprint, exact core version and the three explicit
   operational confirmations. Restart PSP, then use **Install / Reinstall**
   and choose Passwall Node on the **same server record**. Do not add another
   server or rebuild nodes.
7. Confirm the real agent/core reports running and applied config/client state;
   test subscriptions and connections before declaring the migration complete.

Example binary invocation (replace every placeholder; do not paste unchanged):

```sh
psp migrate-server --config /path/to/config.yaml --server-id SERVER_ID --core-version CORE_VERSION
psp migrate-server --config /path/to/config.yaml --server-id SERVER_ID --core-version CORE_VERSION --expected-fingerprint FINGERPRINT --all-psp-stopped --old-xray-stopped --managed-only --apply
```

For a restricted core, include `--allow-restricted-reality` in both invocations.
For Docker Compose, use the configured PSP **service name**, not the container
name; run from the original Compose directory with the same environment:

```sh
docker compose stop YOUR_PSP_SERVICE &&
docker compose run --rm --no-deps YOUR_PSP_SERVICE migrate-server --server-id SERVER_ID --core-version CORE_VERSION
```

Verify that all PSP instances and old Xray have exited, review the dry run, and
replace `FINGERPRINT` with its exact reviewed result. Then apply and start PSP
only if the conversion succeeds:

```sh
docker compose run --rm --no-deps YOUR_PSP_SERVICE migrate-server --server-id SERVER_ID --core-version CORE_VERSION --expected-fingerprint FINGERPRINT --all-psp-stopped --old-xray-stopped --managed-only --apply &&
docker compose start YOUR_PSP_SERVICE
```

`PSP_CONFIG` and the normal database/key env overrides are honored. The command
loads an existing config and requires an already-completed current V4 schema; it
never generates config, initializes SQLite or invokes schema migrations. It does
not print the fixed node credential. Retrieve installation material through the
existing administrator-only installation flow after restart.

Conversion is one database transaction: a pre-commit failure rolls back all backend,
agent, stream, credential, convergence and task changes. If the connection fails
while acknowledging COMMIT, inspect the original server record to determine the
outcome; do not assume it rolled back or restart old Xray blindly. An already-converted server
cannot be converted again and its credential is not rotated by a retry. Prior
node-action tasks for that server are retained with immutable `retired` status;
they cannot be revived by Retry, Cancel or late queue callbacks. Other servers and
ordinary user/mail/certificate tasks are not indiscriminately canceled.

Traffic lifetime/baselines and history are retained; the first native counter
epoch starts a new raw baseline without adding old totals again. Bytes not polled
from old Xray before shutdown cannot be reconstructed. No VM/DB snapshot recovery
workflow or automatic production restart is introduced.
