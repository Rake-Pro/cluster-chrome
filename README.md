# cluster-chrome

Headful Google Chrome on a virtual display, built for Kubernetes and nothing else.
One small image, one tiny device plugin, one probe. No window manager, no VNC, no
init framework, no third-party base image.

| Piece | Image / binary | What it is |
| --- | --- | --- |
| Chrome image | `ghcr.io/rake-pro/cluster-chrome:<semver>` | `debian:trixie-slim` (digest-pinned) + Xvfb + Google Chrome stable (Google's apt repo, key pinned by sha256 and fingerprint) + Mesa Vulkan/VA-API. Runs as uid 1000. `tini` is PID 1. |
| DRI device plugin | `ghcr.io/rake-pro/cluster-chrome-dri-plugin:<semver>` | Advertises `/dev/dri/renderD*` as the extended resource `rake-pro.github.io/dri` so an unprivileged pod gets the GPU render node. Go, `FROM scratch`. |
| chrome-probe | in the image at `/usr/local/bin/chrome-probe` | Connects to Chrome's DevTools and reports `navigator.webdriver`, user agent, WebGL vendor/renderer and friends. CI gate and node check. |

## Why

- Running a real, headful Chrome in a pod (automation that must not look headless) usually means a desktop-in-a-container stack with many moving parts. This repo keeps exactly what Chrome needs: a display, a GPU path, and its own sandbox.
- Chrome's sandbox stays ON. The pod spec enables it through two host profiles (`deploy/node`), not `--no-sandbox`.
- A real GPU renderer: with the render node in the pod, Chrome renders WebGL through ANGLE on Vulkan and reports the actual GPU (e.g. `ANGLE (AMD, ... RADV ...)`) instead of SwiftShader.

## Image contract

| Item | Value |
| --- | --- |
| User | `chrome` (1000:1000), home `/home/chrome` (volume) |
| Display | Xvfb `$DISPLAY` (default `:1`), `$SCREEN` (default `1920x1080x24`), extra `$XVFB_ARGS` |
| Entrypoint | `tini -- entrypoint <cmd>`: starts Xvfb, waits for the socket, execs `<cmd>` |
| Default CMD | `chrome-smoke`: Chrome with DevTools on 127.0.0.1:9222 + `chrome-probe`, exits with the probe status |
| Launcher | `chrome-gpu [chrome args]`: `google-chrome-stable` plus the ANGLE/Vulkan flags when `/dev/dri/renderD*` exists; `CHROME_NO_SANDBOX=1` adds `--no-sandbox` (fallback only) |
| Chrome path | `/usr/bin/google-chrome-stable`; version in `/etc/chrome-version` |
| Healthcheck | X socket present and Xvfb alive |
| Writable paths needed | `/tmp` (emptyDir), `/dev/shm` (>= 512Mi tmpfs), `/home/chrome` |
| Ports | 9222 only if your command enables remote debugging; bind it to 127.0.0.1 |

## Pod requirements

- `securityContext`: `runAsNonRoot`, `readOnlyRootFilesystem`, caps `drop: [ALL]`, `allowPrivilegeEscalation: false`, `supplementalGroups: [<render gid>]`, `seccompProfile` and `appArmorProfile` of type `Localhost` from `deploy/node` (see its README for why and for the no-sandbox fallback).
- GPU: deploy `deploy/k8s/dri-device-plugin.yaml` on the GPU node(s), request `rake-pro.github.io/dri: 1`. Without it Chrome falls back to SwiftShader; `chrome-probe -fail-on-swiftshader` catches that.
- Template: `deploy/k8s/chrome-smoke-pod.yaml`.

## chrome-probe

```
chrome-probe -devtools http://127.0.0.1:9222 -wait 30s -expect-webdriver=false -fail-on-swiftshader
```

- Exit 0 pass, 2 assertion failed, 1 could not talk to Chrome. `-json=false` prints key: value lines; `-url` navigates first.

## Build and release

- `dev` is the working branch; CI builds both images, runs the smoke test twice (sandbox on with the seccomp profile, and the no-sandbox fallback) and Trivy-gates on CRITICAL.
- A promotion PR `dev -> main` (opened automatically by `sync-main.yml`) merges with a merge commit; `release.yml` mints the next semver tag (`release:minor` / `release:major` labels bump more) and pushes both images with `<version>` and `<major.minor>` tags. No `latest` tag.
- `trivy-rescan.yml` rescans the released images weekly at CRITICAL,HIGH.
- Chrome cannot be version-pinned (Google serves only the current stable); every release records the Chrome version it shipped in `/etc/chrome-version` and the image labels. Rebuild by merging any change (or dispatching `release.yml`).
- Go code is vendored; CI fails if `vendor/` drifts from `go.mod`.

## Local

```
docker build -t cluster-chrome:local .
docker run --rm --shm-size=512m --security-opt seccomp=deploy/node/seccomp/cluster-chrome.json cluster-chrome:local
docker run --rm --shm-size=512m --device /dev/dri -e PROBE_ARGS=-fail-on-swiftshader cluster-chrome:local
```
