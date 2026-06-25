# gastown-ci — most restrictive box: locked down for testing against.
# Non-root dev, no passwordless sudo, no writable system. Baked pinned binaries.
ARG BASE=ghcr.io/whiz-digital-vc/devcontainer-base:test-2026-06-22
FROM ${BASE}
USER root
COPY gt bd dolt /usr/local/bin/
RUN chmod 0755 /usr/local/bin/gt /usr/local/bin/bd /usr/local/bin/dolt
LABEL gt.role="ci" gt.baked="pinned gt/bd/dolt; locked-down runtime"
USER dev
