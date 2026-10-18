# Node-side profiles: keeping Chrome's sandbox on

Chrome's renderer sandbox creates user, PID and network namespaces from an
unprivileged process. Two host policies block that inside a plain pod:

| Blocker | Where | What the files here do |
| --- | --- | --- |
| Container runtime seccomp default denies `clone`/`clone3`/`unshare` with namespace flags unless the container has `CAP_SYS_ADMIN` | kubelet `RuntimeDefault` (moby/containerd profile) | `seccomp/cluster-chrome.json` = the moby default profile (v28.3.3) with the two cap-gated clone rules removed and `clone`, `clone3`, `unshare` allowed unconditionally. Everything else identical. |
| Ubuntu 23.10+ `kernel.apparmor_restrict_unprivileged_userns=1`: only processes whose AppArmor profile allows `userns` may create one | host kernel + AppArmor | `apparmor/cluster-chrome` = the runtime's default container profile plus the single rule `userns,`. The sysctl stays at 1. |

- Install on every node that schedules these pods: `sudo bash deploy/node/install.sh` (copies the seccomp profile to `/var/lib/kubelet/seccomp/profiles/`, loads the AppArmor profile). `--remove` undoes it.
- Pod spec: `seccompProfile: {type: Localhost, localhostProfile: profiles/cluster-chrome.json}` and `appArmorProfile: {type: Localhost, localhostProfile: cluster-chrome}`. See `deploy/k8s/chrome-smoke-pod.yaml`.
- Verify from inside: `chrome-smoke` exits 0 and Chrome's log has no `No usable sandbox` / `setuid sandbox` lines.
- Fallback when a host cannot take profiles: run with `CHROME_NO_SANDBOX=1`. The pod (non-root, caps dropped, read-only rootfs, NetworkPolicy) becomes the only boundary; Chrome's per-site renderer isolation is gone. Prefer the profiles.
- Hosts without AppArmor (`apparmor_parser` missing) need only the seccomp profile.
