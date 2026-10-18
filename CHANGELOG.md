# Changelog

Releases are semver tags minted by release.yml on every merge to main; the
section for a version is written when the promotion PR is opened.

## v1.0.0 - unreleased

### Added
- Chrome image: debian trixie-slim (digest-pinned) + Xvfb + Google Chrome stable
  from Google's apt repo (key pinned by sha256 + fingerprint) + Mesa
  Vulkan/VA-API; runs as uid 1000 under tini; `entrypoint`, `chrome-gpu`,
  `chrome-smoke`, `healthcheck`.
- `cmd/dri-device-plugin`: Kubernetes device plugin advertising DRM render
  nodes as `rake-pro.github.io/dri`; `Dockerfile.plugin` (FROM scratch);
  `deploy/k8s/dri-device-plugin.yaml`.
- `cmd/chrome-probe`: CDP fingerprint probe (webdriver, UA, WebGL renderer,
  ...) with assertion flags; shipped in the image.
- `deploy/node`: seccomp + AppArmor profiles that let Chrome keep its own
  sandbox in a pod, with installer and README; `deploy/k8s/chrome-smoke-pod.yaml`.
- CI (build both images, smoke test with sandbox on and the no-sandbox
  fallback, Trivy CRITICAL gate, vendored-module check), release flow
  (dev -> main promotion PR, auto semver, two images), weekly Trivy rescan,
  Dependabot (actions, gomod, docker).
