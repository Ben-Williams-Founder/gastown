#!/usr/bin/env bash
# CONSUMER (RVTM FR-07): UserPromptSubmit hook for mayor+deacon — injects any
# active provenance incident into every coordinator prompt. Dolt-independent,
# microsecond cost (cat only; the producer does all hashing). DEC-OPS-gt-provenance-watchdog.
RED="${WD_STATE_DIR:-$HOME/gt}/PROVENANCE-RED"
[ -f "$RED" ] || exit 0
printf '<system-reminder>\n⛔ PROVENANCE INCIDENT ACTIVE (deterministic watchdog):\n%s\nDo not deploy, do not trust gt behavior, do not clear this file by hand — resolve per the runbook (.claude/rules/gt-build-provenance.md) or ack a known rollback.\n</system-reminder>\n' "$(cat "$RED")"
