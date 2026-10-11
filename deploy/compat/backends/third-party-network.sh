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
