#!/usr/bin/env bash
# Installs the two host-side profiles a cluster-chrome pod references so Chrome
# can keep its own sandbox: a seccomp profile under the kubelet seccomp root
# and an AppArmor profile that allows user namespaces. Run as root on each
# node that will schedule cluster-chrome pods. Idempotent. Changes no sysctl.
#
#   sudo bash deploy/node/install.sh            # install / refresh
#   sudo bash deploy/node/install.sh --remove   # undo
#
# Pod spec afterwards:
#   securityContext:
#     seccompProfile:  {type: Localhost, localhostProfile: profiles/cluster-chrome.json}
#     appArmorProfile: {type: Localhost, localhostProfile: cluster-chrome}
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SECCOMP_ROOT="${SECCOMP_ROOT:-/var/lib/kubelet/seccomp}"
[[ $EUID -eq 0 ]] || { echo "run as root" >&2; exit 1; }
if [[ "${1:-}" == "--remove" ]]; then
  rm -f "${SECCOMP_ROOT}/profiles/cluster-chrome.json"
  if [[ -f /etc/apparmor.d/cluster-chrome ]]; then
    apparmor_parser -R /etc/apparmor.d/cluster-chrome 2>/dev/null || true
    rm -f /etc/apparmor.d/cluster-chrome
  fi
  echo "removed"; exit 0
fi
install -d -m 0755 "${SECCOMP_ROOT}/profiles"
install -m 0644 "${HERE}/seccomp/cluster-chrome.json" "${SECCOMP_ROOT}/profiles/cluster-chrome.json"
echo "seccomp: ${SECCOMP_ROOT}/profiles/cluster-chrome.json"
if command -v apparmor_parser >/dev/null 2>&1; then
  install -m 0644 "${HERE}/apparmor/cluster-chrome" /etc/apparmor.d/cluster-chrome
  apparmor_parser -r -W /etc/apparmor.d/cluster-chrome
  echo "apparmor: profile cluster-chrome loaded ($(aa-status 2>/dev/null | grep -c cluster-chrome || echo '?') entries in aa-status)"
else
  echo "apparmor_parser not found: AppArmor is not in use on this host; omit appArmorProfile from the pod spec" >&2
fi
echo "sysctl kernel.apparmor_restrict_unprivileged_userns = $(sysctl -n kernel.apparmor_restrict_unprivileged_userns 2>/dev/null || echo n/a) (left unchanged)"
