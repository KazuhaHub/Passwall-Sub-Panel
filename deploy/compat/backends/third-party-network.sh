#!/usr/bin/env bash

# Host networking belongs only to the disposable native fleet driver. General
# adapter smoke tests retain their published-port bridge deployment.
third_party_network_mode() {
  case "${PSP_BACKEND_NETWORK:-bridge}" in
    bridge) printf '%s\n' bridge ;;
    host)
      if [ "${GITHUB_ACTIONS:-}" != true ] ||
         [ "${GITHUB_REPOSITORY:-}" != KazuhaHub/Passwall-Sub-Panel ] ||
         [ "${RUNNER_ENVIRONMENT:-}" != github-hosted ] ||
         [[ ! ${PSP_BACKEND_HOST:-} =~ ^10\.254\.([1-9][0-9]{0,2})\.([1-9][0-9]{0,2})$ ]]; then
        echo 'host backend profile requires the dedicated disposable fleet job' >&2
        return 2
      fi
      if (( BASH_REMATCH[1] > 254 || BASH_REMATCH[2] > 254 )); then
        echo 'host backend profile address is outside its owned range' >&2
        return 2
      fi
      printf '%s\n' host
      ;;
    *) echo 'unsupported backend network profile' >&2; return 2 ;;
  esac
}

# Both reviewed panels default to subscription port 2096. They share a network
# namespace only in the fleet fixture; configure the stopped S-UI before its
# readiness check without changing its web endpoint or any credential rows.
third_party_configure_sui_host_db() {
  local privilege=()
  if [ -n "${DB_SUDO:-}" ]; then privilege=("$DB_SUDO"); fi
  "${privilege[@]}" python3 - "$1" <<'PY'
import pathlib, sqlite3, sys

uri = pathlib.Path(sys.argv[1]).resolve().as_uri() + '?mode=rw'
with sqlite3.connect(uri, uri=True) as db:
    db.execute('BEGIN IMMEDIATE')
    row = db.execute("SELECT value FROM settings WHERE key='subPort'").fetchone()
    if row not in [('2096',), ('2097',)]:
        raise RuntimeError('unexpected fixture subscription port')
    changed = db.execute("UPDATE settings SET value='2097' WHERE key='subPort'")
    if changed.rowcount != 1:
        raise RuntimeError('fixture subscription setting is not unique')
PY
}
