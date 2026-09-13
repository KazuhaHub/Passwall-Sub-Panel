# Disposable Linux/systemd acceptance

This gate runs only as root on an **ephemeral GitHub-hosted Ubuntu 24.04 VM**.
It deliberately refuses local machines, self-hosted runners, containers, and
hosts with pre-existing Passwall Node or x-ui installations. Do not bypass it
on a production host. No SSH target or production credential is used.

The build-tagged test uses actual TLS HTTP requests against PSP's production
bootstrap handlers, Bearer authenticator, SQLite repositories, operation gate,
adapter pool, and node-sync coordinator. A fixture-only administrator token and
a pinned catalog entry replace the surrounding login/release-discovery UI; they
do not replace the downloaded installer, node binary, core runtime, or systemd.
The installer downloads the already-published **v0.0.1-beta3** Node archive and
verifies its release checksum and exact executable version.
Its executable and observed report must also match the publisher's exact
`v0.0.1-beta3 (91c36bf)` build stamp, not merely a bare version string.

Required checks are:

- Partial old deployments and stale fingerprints fail before database conversion.
- Clean systemd-node conversion using the exact command minted by PSP.
- Standard x-ui unit/config/database backup and shutdown, then recovery after an
  intentional installer-lock failure, without restarting the old backend.
- Real Node service, real Xray 26.6.27, applied config/roster/directives, and an
  HTTP request through a real VLESS client/server pair. Durable PSP heartbeat
  receipt must be newer than the installation command, not a cached old-machine
  acknowledgement or node-supplied timestamp.
- Ordinary reinstall preserves the service PID, fixed credential, static files,
  SQLite state bytes/inode while paused, and PSP IDs/credentials/counter history.
- Removing only this run's owned Node installation simulates an OS reinstall;
  reinstalling the original server rehydrates the same PSP identity and proxies.

The old x-ui deployment is an explicitly labeled **systemd/SQLite fixture**, not
the real 3X-UI server. This gate does not claim a real 3X-UI or S-UI migration
handshake, third-party automatic recovery, distributed PSP coordination, or
production firewall/database/VM rollback acceptance. Unit/integration tests
cover unsupported upstream choices separately.

Run only through `node-reinstall-acceptance.yml`. Ordinary `go test ./...` does
not compile this mutating acceptance program. Successful acceptance must include
the named test running and passing; a skipped/not-run test is not evidence.
