# syntax=docker/dockerfile:1
#
# cluster-chrome: headful Google Chrome on a virtual display, for Kubernetes.
#
#   debian-slim (pinned by digest) + Xvfb + Google Chrome stable (Google's own
#   apt repo, signing key pinned by checksum AND fingerprint) + Mesa Vulkan so
#   Chrome renders on a real GPU when a DRM render node is handed to the pod.
#
# Nothing else: no window manager, no VNC, no init system beyond tini. The
# process you run (CMD) owns the display; see rootfs/usr/local/bin for the
# entrypoint, the GPU-aware launcher and the smoke test. Runs as uid 1000,
# never root. Chrome's own sandbox stays ON; the pod spec enables it (see
# deploy/node and README "Chrome sandbox").
#
# Image build inputs that cannot be pinned to a version: Chrome itself (Google
# keeps only the current stable in the repo). The installed version is stamped
# into the image label org.opencontainers.image.version.chrome at build time.

############################
# Stage: probe (Go) - builds the CDP fingerprint probe shipped in the image.
############################
ARG DEBIAN_DIGEST=sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f
ARG GO_VERSION=1.27.2
FROM golang:${GO_VERSION}-trixie AS probe
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY cmd/chrome-probe ./cmd/chrome-probe
RUN CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags "-s -w" -o /out/chrome-probe ./cmd/chrome-probe

############################
# Stage: runtime
############################
FROM debian@${DEBIAN_DIGEST} AS runtime
ARG GOOGLE_KEY_SHA256=54dea5f6c2a26091578cf52a999cebc6b64df478d37ad4dce96376b711e3b27c
ARG GOOGLE_KEY_FPR=EB4C1BFD4F042F6DDDCCEC917721F63BD38B4796
ARG PUID=1000
ARG PGID=1000
SHELL ["/bin/bash", "-euo", "pipefail", "-c"]
ENV DEBIAN_FRONTEND=noninteractive

# Runtime packages, all from Debian:
#   xvfb                 the virtual X server
#   tini                 PID 1 (signal forwarding, zombie reaping)
#   libvulkan1 + mesa-vulkan-drivers   ANGLE-on-Vulkan GPU path (radv/anv/lavapipe)
#   libva + mesa-va-drivers            VA-API video decode on the same node
#   libgl1-mesa-dri      GLX fallback for Xvfb (llvmpipe) when no GPU is present
#   fonts-*              enough glyph coverage for real sites
#   procps, curl         healthcheck (pgrep) and the smoke test (curl)
# gnupg is used once to dearmor the Google key and purged again.
RUN apt-get update \
 && apt-get upgrade -y \
 && apt-get install -y --no-install-recommends \
      ca-certificates curl gnupg tini procps \
      xvfb libgl1-mesa-dri \
      libvulkan1 mesa-vulkan-drivers libva2 libva-drm2 mesa-va-drivers \
      fonts-liberation fonts-dejavu-core fonts-noto-color-emoji \
 && curl -fsSL https://dl.google.com/linux/linux_signing_key.pub -o /tmp/google.asc \
 && echo "${GOOGLE_KEY_SHA256}  /tmp/google.asc" | sha256sum -c - \
 && gpg --show-keys --with-colons /tmp/google.asc | grep -q "^fpr:::::::::${GOOGLE_KEY_FPR}:" \
 && install -d -m 0755 /etc/apt/keyrings \
 && gpg --dearmor -o /etc/apt/keyrings/google-chrome.gpg /tmp/google.asc \
 && echo 'deb [arch=amd64 signed-by=/etc/apt/keyrings/google-chrome.gpg] https://dl.google.com/linux/chrome/deb/ stable main' \
      > /etc/apt/sources.list.d/google-chrome.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends google-chrome-stable \
 && apt-get purge -y gnupg \
 && apt-get autoremove -y \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/* \
 # The Chrome deb installs a daily cron that re-adds Google's repo and a
 # duplicate sources entry; neither belongs in an immutable image.
 && rm -f /etc/cron.daily/google-chrome /opt/google/chrome/cron/google-chrome \
 && printf 'deb [arch=amd64 signed-by=/etc/apt/keyrings/google-chrome.gpg] https://dl.google.com/linux/chrome/deb/ stable main\n' \
      > /etc/apt/sources.list.d/google-chrome.list \
 && google-chrome-stable --version | tee /etc/chrome-version

RUN groupadd -g "${PGID}" chrome \
 && useradd -m -u "${PUID}" -g "${PGID}" -s /usr/sbin/nologin chrome

COPY --from=probe /out/chrome-probe /usr/local/bin/chrome-probe
COPY rootfs/ /
RUN chmod 0755 /usr/local/bin/entrypoint /usr/local/bin/chrome-gpu /usr/local/bin/chrome-smoke /usr/local/bin/healthcheck

ENV DISPLAY=:1 \
    SCREEN=1920x1080x24 \
    HOME=/home/chrome \
    LANG=C.UTF-8

USER chrome
WORKDIR /home/chrome
VOLUME ["/home/chrome"]
EXPOSE 9222
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/entrypoint"]
CMD ["chrome-smoke"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 CMD /usr/local/bin/healthcheck
