# gastown-townhost — full self-contained town for B-hosting (OCI A1 arm64).
# systemd as PID 1 (cgroup delegation). Baked pinned gt/bd/dolt.
# NOTE: run with `--entrypoint /sbin/init` (the base ENTRYPOINT is tini, which
# would hijack PID 1 — see DEC-OPS-town-host-container-deployment runbook).
ARG BASE=ghcr.io/whiz-digital-vc/devcontainer-full:ce01652430a0
FROM ${BASE}
USER root
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      systemd systemd-sysv dbus \
 && rm -rf /var/lib/apt/lists/*
COPY gt bd dolt /usr/local/bin/
RUN chmod 0755 /usr/local/bin/gt /usr/local/bin/bd /usr/local/bin/dolt
ENV GT_TOWN_HOST=1
LABEL gt.role="townhost" gt.baked="pinned gt/bd/dolt; systemd-PID1"
CMD ["/sbin/init"]
