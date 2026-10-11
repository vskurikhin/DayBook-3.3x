# ---------------------------------------------------------------------------
# Inline orchestration verification (ADR-004 / architecture R3.7)
# ---------------------------------------------------------------------------
#
# verify-workflow runs the orchestration assertions for dispatch, failure
# propagation, locking, serialization and missing-tool handling. It coordinates
# isolated sub-checks; each sub-check owns its own lock. No Docker, services or
# databases are used. The Docker lifecycle (verify-docker) is a later approved
# increment (TASK-004).
#
# Test seams (.verify-*) are internal; ordinary public targets never call them,
# so public commands cannot bypass locks or preflight checks.

verify-workflow:
	@echo '=== verify: help is static (no lock, no state) and lists targets ==='
	@tmp=$$(mktemp); \
	lock_before=0; host_before=0; \
	[ -e "$(LOCK)" ] && lock_before=1; \
	[ -e "$(HOSTDIR)" ] && host_before=1; \
	$(MAKE) -s help > "$$tmp"; \
	ok=1; \
	for t in $(PUBLIC_TARGETS); do grep -q "^  $$t" "$$tmp" || { echo "FAIL: help missing target $$t"; ok=0; }; done; \
	rm -f "$$tmp"; \
	[ $$ok -eq 1 ] || exit 1; \
	lock_after=0; host_after=0; \
	[ -e "$(LOCK)" ] && lock_after=1; \
	[ -e "$(HOSTDIR)" ] && host_after=1; \
	{ [ $$lock_before -eq $$lock_after ] && [ $$host_before -eq $$host_after ]; } \
		|| { echo "FAIL: help created or removed workflow state"; exit 1; }
	@echo 'PASS: help'
	@echo '=== verify: test-app reports missing capability and fails ==='
	@$(MAKE) -s test-app >/dev/null 2>&1; rc=$$?; \
	test $$rc -ne 0 || { echo 'FAIL: test-app did not fail'; exit 1; }
	@echo 'PASS: test-app capability gap'
	@echo '=== verify: aggregate build includes all three component steps ==='
	@grep -q '\$$(JAVA_ALL_BUILD) && \$$(AUTH_BUILD) && \$$(APP_BUILD)' "$(ROOT)/Makefile" \
		|| { echo 'FAIL: aggregate build missing a component step (ordering/prerequisite)'; exit 1; }
	@echo 'PASS: aggregate completeness'
	@echo '=== verify: missing tool rejected (nonzero, non-secret) ==='
	@$(MAKE) -s .verify-missing-tool
	@echo 'PASS: missing tool'
	@echo '=== verify: child failure stops dependents and releases lock ==='
	@$(MAKE) -s .verify-fail-fast
	@echo 'PASS: failure propagation'
	@echo '=== verify: occupied lock rejects a competing operation ==='
	@$(MAKE) -s .verify-competing
	@echo 'PASS: competing rejection'
	@echo '=== verify: make -j serializes protected operations ==='
	@$(MAKE) -s .verify-j
	@echo 'PASS: -j serialization'
	@echo '=== verify: caught interruption retains the lock ==='
	@$(MAKE) -s .verify-interrupt
	@echo 'PASS: interruption retains lock'
	@rm -f "$(HOSTDIR)/a" "$(HOSTDIR)/b"
	@echo '=== verify: strict config accepts valid input + core key byte map ==='
	@$(MAKE) -s .verify-config-valid
	@echo 'PASS: config valid + byte map'
	@echo '=== verify: forbidden secret overrides are rejected ==='
	@$(MAKE) -s .verify-config-secret-override
	@echo 'PASS: secret override rejection'
	@echo 'verify-workflow: ALL CHECKS PASSED'

.PHONY: .verify-missing-tool .verify-fail-fast .verify-competing .verify-j .verify-interrupt .hold-lock-int

.verify-missing-tool:
	@$(MAKE) -s .missing-tool-seam 2>/dev/null; rc=$$?; \
	test $$rc -ne 0 || { echo 'FAIL: missing tool not rejected'; exit 1; }; \
	rmdir "$(LOCK)" 2>/dev/null || true

.PHONY: .missing-tool-seam
.missing-tool-seam:
	$(call run_locked,$(call check_tool,no-such-tool-daybook-verify))

.verify-fail-fast:
	@rm -f "$(WORKFLOW)/.sentinel"
	@$(MAKE) -s .fail-fast-seam 2>/dev/null; rc=$$?; \
	test $$rc -ne 0 || { echo 'FAIL: failing child reported success'; exit 1; }; \
	{ [ ! -e "$(WORKFLOW)/.sentinel" ] && [ ! -e "$(LOCK)" ]; } || { echo 'FAIL: dependent ran or lock retained after failure'; exit 1; }

.PHONY: .fail-fast-seam
.fail-fast-seam:
	$(call run_locked,false && touch "$(WORKFLOW)/.sentinel")

# Simulate an occupied lock (as a separate competing process would leave it) and
# confirm a public-style operation is rejected without modifying state.
.verify-competing:
	@rm -rf "$(LOCK)"; mkdir -p "$(WORKFLOW)"
	@mkdir "$(LOCK)"   # occupy the lock, mimicking a running competitor
	@$(MAKE) -s .competing-seam 2>/dev/null; rc=$$?; \
	test $$rc -ne 0 || { echo 'FAIL: competing operation not rejected'; exit 1; }; \
	rmdir "$(LOCK)"

.PHONY: .competing-seam
.competing-seam:
	$(call run_locked,true)

# Two protected goals under -j must run serially (no overlap) and leave the lock
# released. .NOTPARALLEL guarantees serialization; the check confirms success and
# release.
.verify-j:
	@$(MAKE) -s -j2 .j-seam-a .j-seam-b
	@{ [ ! -e "$(LOCK)" ] && [ -e "$(HOSTDIR)/a" ] && [ -e "$(HOSTDIR)/b" ]; } \
		|| { echo 'FAIL: -j serialization or lock release'; exit 1; }

.PHONY: .j-seam-a .j-seam-b
.j-seam-a:
	$(call run_locked,mkdir -p "$(HOSTDIR)" && touch "$(HOSTDIR)/a")
.j-seam-b:
	$(call run_locked,mkdir -p "$(HOSTDIR)" && touch "$(HOSTDIR)/b")

# A background holder acquires the lock and sleeps; INT must retain the lock for
# manual diagnosis (never release while a child may still write). Bounded wait.
.verify-interrupt:
	@rm -rf "$(LOCK)" "$(WORKFLOW)/.int-done"
	@$(MAKE) -s .hold-lock-int >/dev/null 2>&1 &
	@pid=$$!; \
	n=0; while [ ! -e "$(LOCK)" ] && [ $$n -lt 50 ]; do sleep 0.1; n=$$((n+1)); done; \
	[ -e "$(LOCK)" ] || { echo 'FAIL: holder never acquired lock'; kill $$pid 2>/dev/null; exit 1; }; \
	kill -INT $$pid 2>/dev/null || true; \
	wait $$pid 2>/dev/null; \
	{ [ -e "$(LOCK)" ] && [ ! -e "$(WORKFLOW)/.int-done" ]; } \
		|| { echo 'FAIL: lock not retained after interruption'; exit 1; }; \
	rmdir "$(LOCK)"

.PHONY: .verify-config-valid .verify-config-secret-override .verify-config-ambient

# Valid config accepted and the derived core key equals padded Base64 of the raw
# token bytes.
.verify-config-valid:
	@td=$$(mktemp -d); \
	PW='verify-password-0123456789-abcdefghijklmnopqrstuvwxyz'; \
	TK='verify-token-0123456789-abcdefghijklmnopqrstuvwxyz'; \
	printf 'LOCAL_DB_PASSWORD=%s\nLOCAL_TOKEN_KEY=%s\nIMAGE_PREFIX=daybook-local\nIMAGE_TAG=dev\nCOMPOSE_PROJECT_NAME=daybook-local\nDB_PORT=35433\nAUTH_PORT=38090\nCORE_PORT=38083\nAPI_PORT=38082\nAPP_PORT=30081\nSTARTUP_TIMEOUT=90\n' "$$PW" "$$TK" > "$$td/good.env"; \
	out="$$($(MAKE) -s .config-corekey-seam ENV_FILE="$$td/good.env" 2>&1)"; rc=$$?; \
	[ "$$rc" -eq 0 ] || { echo "FAIL: valid config rejected: $$out"; rm -rf "$$td"; exit 1; }; \
	exp="$$(printf '%s' "$$TK" | base64 | tr -d '\n')"; \
	case "$$out" in *"CORE=$$exp"*) ;; *) echo "FAIL: core key byte map mismatch (got $$out)"; rm -rf "$$td"; exit 1 ;; esac; \
	\
	pout="$$($(MAKE) -s .config-publish-seam ENV_FILE="$$td/good.env" 2>&1)"; prc=$$?; \
	[ "$$prc" -eq 0 ] || { echo "FAIL: publish failed: $$pout"; rm -rf "$$td"; exit 1; }; \
	pruntime="$$(printf '%s' "$$pout" | sed -n 's/^RUNTIME=//p')"; \
	[ -n "$$pruntime" ] && [ -f "$$pruntime/env" ] || { echo 'FAIL: published env missing'; rm -rf "$$td"; exit 1; }; \
	grep -q "^CORE_SERVER_TOKEN_SIGNING_KEY=$$exp$$" "$$pruntime/env" \
		|| { echo 'FAIL: published core key != padded Base64 of raw token'; rm -rf "$$td"; exit 1; }; \
	rm -rf "$$td"

# Forbidden secret overrides (command line and environment) are rejected.
.verify-config-secret-override:
	@td=$$(mktemp -d); \
	PW='verify-password-0123456789-abcdefghijklmnopqrstuvwxyz'; \
	TK='verify-token-0123456789-abcdefghijklmnopqrstuvwxyz'; \
	printf 'LOCAL_DB_PASSWORD=%s\nLOCAL_TOKEN_KEY=%s\n' "$$PW" "$$TK" > "$$td/good.env"; \
	$(MAKE) -s .config-parse-run ENV_FILE="$$td/good.env" LOCAL_DB_PASSWORD=fromcmdline >/dev/null 2>&1; rc=$$?; \
	[ "$$rc" -ne 0 ] || { echo 'FAIL: command-line secret override not rejected'; rm -rf "$$td"; exit 1; }; \
	LOCAL_TOKEN_KEY=fromenv $(MAKE) -s .config-parse-run ENV_FILE="$$td/good.env" >/dev/null 2>&1; rc2=$$?; \
	[ "$$rc2" -ne 0 ] || { echo 'FAIL: environment secret override not rejected'; rm -rf "$$td"; exit 1; }; \
	rm -rf "$$td"

.PHONY: .hold-lock-int
.hold-lock-int:
	$(call run_locked,sleep 30 && touch "$(WORKFLOW)/.int-done")
