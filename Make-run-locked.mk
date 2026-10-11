# ---------------------------------------------------------------------------
# Operation lock (ADR-001 / architecture R3.6)
# ---------------------------------------------------------------------------
#
# run_locked acquires one atomic checkout-wide lock, runs the given fragment,
# and releases the lock only after the operation (and its children) completes.
# On INT/TERM the lock is retained for manual diagnosis rather than released
# while a child may still be writing. A competing root operation fails with a
# non-secret diagnostic. Stale locks after SIGKILL must be removed manually
# after confirming no owner process remains; this Makefile never auto-steals.
#
# Usage:  $(call run_locked,<fragment>)

define run_locked
@set -e; \
mkdir -p "$(WORKFLOW)"; \
lockdir="$(LOCK)"; \
retain=0; \
acquired=0; \
trap 'rc=$$?; if [ "$$retain" -eq 0 ] && [ "$$acquired" -eq 1 ]; then rmdir "$$lockdir" 2>/dev/null || true; fi; exit $$rc' EXIT; \
trap 'retain=1; echo "interrupted: operation lock retained at $(LOCK); remove it manually only after confirming no owner process remains" >&2; exit 130' INT TERM HUP; \
if ! mkdir "$$lockdir" 2>/dev/null; then \
  echo "error: operation lock is held: $(LOCK)" >&2; \
  echo "another root Makefile operation is running, or a stale lock remains" >&2; \
  echo "(remove it manually only after confirming no owner process remains)" >&2; \
  exit 1; \
fi; \
acquired=1; \
$(1)
endef
