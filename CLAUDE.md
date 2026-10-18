# cluster-chrome

Hardened headful Chrome base image + DRI device plugin + CDP probe for Kubernetes.
Public repo: no internal hostnames, addresses, cluster or private repo names anywhere
(code, comments, workflows, docs, commit messages).

## Non-negotiable constraints

- No third-party base images. `FROM debian@sha256:...` only, digest pinned, bumped on purpose.
- Official sources only: Debian apt and Google's Chrome apt repo. The Google key is
  pinned by sha256 AND fingerprint in the Dockerfile; a key rotation is a deliberate
  Dockerfile change, never a silent `curl | apt-key`.
- Non-root at runtime (uid 1000), no sudo/gosu, read-only rootfs friendly. Chrome's own
  sandbox is never disabled by default; `CHROME_NO_SANDBOX=1` is the documented fallback.
- Go: standard library first; allowed extras are `golang.org/x/*`, `google.golang.org/*`,
  `k8s.io/kubelet` (device plugin API) and `github.com/rs/zerolog` (logging). Vendored
  (`go mod vendor` committed); CI fails on drift.
- No window managers, VNC, noVNC, s6 or desktop stacks. The image gives a display and a
  GPU path; the workload brings the app.
- Semver tags only (`vX.Y.Z` minted by release.yml). No `latest`, no sha tags.
- Trivy CRITICAL gate on CI and release; `.trivyignore.yaml` entries must be file-scoped
  and expire.

## Layout

| Path | What |
| --- | --- |
| `Dockerfile`, `rootfs/` | Chrome image and its scripts (`entrypoint`, `chrome-gpu`, `chrome-smoke`, `healthcheck`) |
| `Dockerfile.plugin`, `cmd/dri-device-plugin` | DRI render-node device plugin (FROM scratch) |
| `cmd/chrome-probe` | DevTools fingerprint probe, built into the image |
| `deploy/node` | seccomp + AppArmor profiles, installer, rationale |
| `deploy/k8s` | device plugin DaemonSet, smoke-test pod (security-context template) |
| `.github/workflows` | ci (dev), sync-main (promotion PR), release (main), trivy-rescan (weekly) |

## Working rules

- Stage only; never commit or push unless told. `dev` is the working branch, `main` is
  what releases; promotion is a merge-commit PR.
- Keep the Dockerfile explainable line by line; every package has a reason in the comment above it.
- Docs are bullets and tables, not prose.
