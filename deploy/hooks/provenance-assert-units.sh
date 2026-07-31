#!/usr/bin/env bash
# SessionStart assertion (RVTM SR-02; replaces refuted layer B's only margin):
# warn loudly if the watchdog units are not live. Exit 0 always (advisory).
for u in provenance-watch.timer provenance-watch.path; do
  systemctl --user is-active --quiet "$u" 2>/dev/null || \
    echo "⚠️ provenance watchdog unit NOT active: $u — bypass detection degraded; re-enable: systemctl --user enable --now $u"
done
exit 0
