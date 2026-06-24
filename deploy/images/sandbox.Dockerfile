# gastown-sandbox — experiment box: mirrors dev-linux-01 closest.
# Writable root + sudo, NO systemd. Baked pinned gt/bd/dolt (no build-on-boot).
ARG BASE=ghcr.io/whiz-digital-vc/devcontainer-base-sandbox:test-2026-06-22
FROM ${BASE}
USER root
COPY gt bd dolt /usr/local/bin/
RUN chmod 0755 /usr/local/bin/gt /usr/local/bin/bd /usr/local/bin/dolt
LABEL gt.role="sandbox" gt.baked="pinned gt/bd/dolt; build-on-boot disabled"
USER dev
