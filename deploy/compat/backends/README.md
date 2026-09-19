# Isolated third-party backends

`third-party.sh` starts a real 3X-UI and a real S-UI panel in containers, mints a
credential for each, seeds what the tests need, and prints the environment those
tests read.

```bash
deploy/compat/backends/third-party.sh up     # pull, run, wait, mint, seed
eval "$(deploy/compat/backends/third-party.sh env)"
node deploy/compat/check-go-results.mjs --profile third-party-3xui-live \
  --input evidence/3xui.jsonl --go-exit-code "$code" --output evidence/3xui.json
deploy/compat/backends/third-party.sh down   # removes only this case's containers
```

Measured on `ghcr.io/mhsanaei/3x-ui:3.8.5` (Xray 26.9.9) and
`ghcr.io/alireza0/s-ui:1.6.3`, from an empty host:

`env` prints `export` statements, because the documented way to consume it is
`eval` and the consumer is a **child** process: `go test` inherits the exported
environment only, so a bare `NAME=value` would set a shell variable, leave every
test to SKIP for want of a variable, and pass any check the evaluating shell
could make about itself. Values are quoted with `%q` — a token or a path is
data, and evaluated unquoted a space word-splits it and a `$(...)` runs.
`third-party_env.test.mjs` asserts both properties from a child process, and the
workflow re-checks them with `printenv`, which is a child too.

| Suite | Pass | N/A | Fail |
| --- | --- | --- | --- |
| 3X-UI adapter | 9 | 2 | 0 |
| S-UI adapter | 1 | 0 | 0 |

Both ship at the exact versions `docs/compat/v4-ranges.json` records as the
tested ceilings.

## The two probes that skip, and why a waiver has to be REQUIRED to count

The fail2ban probes need a fail2ban endpoint the panel image does not run, so
they skip. They are declared `notApplicable` in `profiles-third-party.json` with
a reason, so they are reported **N/A and never counted as coverage** — the
alternative, leaving them to skip, is indistinguishable from a pass to anything
reading the exit code, which is the failure `check-go-results.mjs` exists to
prevent.

**They must be listed in `required` as well as waived**, and that is not
redundant. A waiver only attaches to an item the profile already requires
(`if (!expected.has(name)) continue`); waived-but-not-required is inert, so
nothing reports as N/A and the skipped test instead reports as **unexpected** —
which fails the run when it is top-level. While the launcher exported nothing,
every test skipped and the run failed as `skip`, which hid this; the moment the
environment reached `go test`, the same profile produced `unexpected-test`.
`deploy/compat/profiles.test.mjs` holds that invariant over every shipped
profile, because the validator cannot: its own reasoning — an ignored waiver
leaves "the item still required" — holds only when the name is a typo of a
required one, not when the item is absent from `required` altogether.

## Things the images do not tell you

Each of these cost a run to find, and each produces a failure that reads like a
broken adapter rather than a missing setup step.

- **3X-UI 3.8.x keeps API tokens in a dedicated `api_tokens` table** (name,
  scope, expiry) and only shows the plaintext once, so the token has to be
  created through the panel — which means a cookie session *and* the
  `X-CSRF-Token` header, because the login POST now refuses without it. Inserting
  a row directly does not work: the stored value is not the presented one.
- **The shared-client tests skip below two inbounds.** A fresh panel has none, so
  the launcher seeds two. Without them five tests report SKIP and the suite still
  exits 0.
- **The traffic-floor matrix needs `PSP_LIVE_XUI_DB` and a writable DIRECTORY**,
  not just a writable file: it seeds usage straight into the panel's SQLite, and
  SQLite creates its journal beside the file. The panel runs as its own root, so
  both belong to uid 0 and `up` hands the folder to the invoking user — without
  that, the open fails as `unable to open database file: out of memory (14)`,
  which reads like a corrupt database rather than a permission on the folder.
  This went unnoticed because the only runs that had ever reached the tests were
  from a root shell, where the folder was writable anyway.
- **S-UI serves the panel under a configurable `webPath`** — `/app/` on a fresh
  install — so the API is at `/app/apiv2/...`. The launcher reads the value out of
  the database rather than assuming it, and waits on the real base: the bare
  origin 404s however healthy the panel is.
- **S-UI's `tokens.expiry` is in seconds**, where PSP's own expiry fields are
  milliseconds. `0` means no expiry.

## Scope

- The panels are pinned by the caller through `PSP_LIVE_3XUI_IMAGE` /
  `PSP_LIVE_SUI_IMAGE`. Nothing here chooses a version, and nothing writes to
  `docs/compat/v4-ranges.json` — that file is a record of what a human verified.
- `down` removes this case's containers only. It never prunes globally.
- The runtime is discovered (`docker`, `nerdctl`, `podman`) with `sudo` added only
  when the runtime cannot be reached without it.
- This is a **surface** check. It says the adapter talks to a real panel
  correctly; it does not say a subscription rendered by PSP connects through a
  real core, which is a different evidence type and a different task.
