include .env

PROJECTNAME=$(shell basename "$(PWD)")

# Go related variables.
GOBASE=$(shell pwd)
GOPATH="$(GOBASE)/vendor:$(GOBASE)"
GOBIN=$(GOBASE)/bin
GOCMD=$(GOBASE)/cmd
TRACE=trace
TRACE_LOG_LEVEL=0

# Redirect error output to a file, so we can show it in development mode.
STDERR=/tmp/.$(PROJECTNAME)kv-stderr.txt

NODES := 1 2 3

# PID file will keep the process id of the raft
pid_file   = /tmp/.$(PROJECTNAME)kv-$(1).pid
PID_LOADKV = /tmp/.loadkv.pid

trace_cm   = $(GOBASE)/$(TRACE)/$(PROJECTNAME)cm-$(1).trace
trace_kv   = $(GOBASE)/$(TRACE)/$(PROJECTNAME)kv-$(1).trace
stderr     = $(GOBASE)/$(TRACE)/$(PROJECTNAME)kv-$(1).stderr
stdout     = $(GOBASE)/$(TRACE)/$(PROJECTNAME)kv-$(1).stdout

# Списки всех pid/trace/stderr файлов, собираются из NODES:
PID_FILES     = $(LOADKV_PID) $(foreach n,$(NODES),$(call pid_file,$(n)))
TRACE_FILES   = $(foreach n,$(NODES),$(call trace_cm,$(n)) $(call trace_kv,$(n)))
STDERR_FILES  = $(foreach n,$(NODES),$(call stderr,$(n)))

OUT_LOAD_KV_FILE = $(GOBASE)/$(TRACE)/loadkv.out

HTTP_PORTS = 8881 8882 8883
RAFT_PORTS = 9991 9992 9993
http_port = $(word $(1),$(HTTP_PORTS))
raft_port = $(word $(1),$(RAFT_PORTS))

TEMP_FILE=$(shell mktemp)
MAIN_GO=./$(GOCMD)/main.go

PKG?=./...
TESTFLAGS?=
RACE_PKGS := . ./pkg/...
LINTFLAGS?=
COUNT?=10

# Make is verbose in Linux. Make it silent.
MAKEFLAGS += --silent

## coverage: Calculate coverage
coverage: go-coverage

## test: Run all tests (PKG=./... TESTFLAGS="")
test: go-test

## test-race: Run all tests with the race detector (PKG=./... TESTFLAGS="")
test-race: go-test-race

## test-stress: Run the stress gate (COUNT sequential -race runs, default 10); detects low-frequency flaky tests, a single run cannot
test-stress: go-test-stress-race

## lint: Run golangci-lint (config .golangci.yml, requires v2.12.2)
lint: go-lint

## start: Start in development mode. Auto-starts when code changes.
start: start-raft

## stop: Stop development mode. RAFT
stop: stop-raft

start-raft: stop-raft
	@for n in $(NODES); do \
		http=$$(printf '888%s' $$n); \
		rpc=$$(printf '999%s' $$n); \
		peers=$$(for p in $(NODES); do \
			[ $$p = $$n ] || printf '%s=:999%s,' $$p $$p; done | sed 's/,$$//'); \
		echo "  >  $(PROJECTNAME) is available at $$http"; \
		$(GOBIN)/$(PROJECTNAME)kv -number $$n \
			-http-addr=":$$http" -rpc-addr=":$$rpc" -peers="$$peers" \
			-trace-log-level $(TRACE_LOG_LEVEL) \
			--trace-cm-log-file "$(call trace_cm,$$n)" \
			--trace-kv-log-file "$(call trace_kv,$$n)" \
			1>"$(call stdout,$$n)" 2>"$(call stderr,$$n)" & \
		echo $$! > $(call pid_file,$$n); \
		sed "/^/s/^/  \>  PID$$n: /" $(call pid_file,$$n); \
	done
	@sleep 2
	@$(GOBIN)/loadkv \
		-concurrency 10 \
		-get-percent 75 \
		-peers ":8881,:8882,:8883" \
		-request-rate 700 \
		> $(OUT_LOAD_KV_FILE) 2>&1 & echo $$! > $(PID_LOADKV)
	@sed "/^/s/^/  \>  PID4: /" $(PID_LOADKV)

stop-raft:
	@-touch $(PID_LOADKV)
	@-kill `cat $(PID_LOADKV)` 2> /dev/null || true
	@-rm $(PID_LOADKV)
	@sleep 3
	@for f in $(PID_FILES); do \
		touch $$f; kill `cat $$f` 2> /dev/null || true; rm $$f; \
	done

restart-raft: stop-raft start-raft

## build: Compile the binary.
build: go-build-raft

## clean: Clean build files. Runs `go clean` internally.
clean: clean-build-files clean-data-files clean-trace-files go-clean go-clean-test-cache
	@rm -f $(PID_FILES)

clean-build-files:
	@echo "  >  Clean build files..."
	@rm -f $(GOBIN)/loadkv $(GOBIN)/$(PROJECTNAME)kv

clean-data-files:
	@echo "  >  Clean data files..."
	@rm -f ./data/node-?/snapshots/*/*
	@rm -f ./data/node-?/* 2>/dev/null || true
	@rmdir ./data/node-?/snapshots/* 2>/dev/null || true

clean-trace-files:
	@echo "  >  Clean trace files..."
	@rm -f $(OUT_LOAD_KV_FILE) \
		$(wildcard $(GOBASE)/$(TRACE)/$(PROJECTNAME)cm-*.trace) \
		$(wildcard $(GOBASE)/$(TRACE)/$(PROJECTNAME)kv-*.trace) \
		$(wildcard $(GOBASE)/$(TRACE)/$(PROJECTNAME)kv-*.stderr) \
		$(wildcard $(GOBASE)/$(TRACE)/$(PROJECTNAME)kv-*.stdout) \
		$(STDERR)

go-compile: go-build-raft

go-build-raft:
	@echo "  >  Building $(PROJECTNAME)kv binary..."
	@cd $(GOCMD) && go build -o $(GOBIN)/$(PROJECTNAME)kv .
	@echo "  >  Building loadkv binary..."
	@cd $(GOCMD)/load && go build -o $(GOBIN)/loadkv .

go-coverage:
	@echo "  >  Generate the list of packages to include..."
	# 1. Generate the list of packages to include, excluding 'mocks'
	# go list ./... lists all packages; grep -v excludes lines containing 'mocks'
	CVPKG=$(go list ./... | grep -v -e '/part\|/cmd' | tr '\n' ',')
	# 2. Run tests with coverage analysis applied only to the specified packages
	@echo "  >  Run tests with coverage analysis..."
	@go test -coverprofile=coverage.out . ./pkg
	# 3. Generate the HTML report
	@echo "  >  Generate the HTML report."
	@go tool cover -html=coverage.out -o docs/coverage.html

verify-imports:
	@if grep -rn --include='*.go' -e 'vskurikhin/raft/pkg/raft"' \
		--exclude-dir=part1 --exclude-dir=part2 --exclude-dir=part3 \
		--exclude-dir=part4kv --exclude-dir=part5kv --exclude-dir=vendor \
		--exclude-dir=.doc --exclude-dir=.ai . ; then \
		echo "found imports of removed path vskurikhin/raft/pkg/raft" >&2; \
		exit 1; \
	fi

go-test: verify-imports go-test-root go-test-tracetest go-test-pkg go-test-kvservice

verify-race-scope:
	@ROOT_PKG=$$(go list -m); \
	go list $(RACE_PKGS) | grep -qx "$$ROOT_PKG" || { \
		echo "race scope lost root raft package: $$ROOT_PKG not in $(RACE_PKGS)" >&2; \
		exit 1; \
	}

go-test-kvservice:
	@echo "  >  Running tests: pkg/kvservice/... $(TESTFLAGS)"
	@go test $(TESTFLAGS) ./pkg/kvservice/...

go-test-pkg:
	@echo "  >  Running tests: pkg $(TESTFLAGS)"
	@go test $(TESTFLAGS) ./pkg

# Stress-прогон: последовательные повторы под -race вне pre-merge пути.
# COUNT задаёт число прогонов (по одной задаче может требоваться больше);
# -parallel не применяется — параллельность исказила бы воспроизводимость.
go-test-stress-race: verify-race-scope
	@echo "  >  Running stress tests with -race (count=$(COUNT)): $(RACE_PKGS) $(TESTFLAGS)"
	@go test -race -count=$(COUNT) -timeout 60m $(TESTFLAGS) $(RACE_PKGS)

go-test-race: verify-race-scope
	@echo "  >  Running tests with -race: $(RACE_PKGS) $(TESTFLAGS)"
	@go test -race $(TESTFLAGS) $(RACE_PKGS)

go-test-root:
	@echo "  >  Running tests: . $(TESTFLAGS)"
	@go test $(TESTFLAGS) .

go-test-tracetest:
	@echo "  >  Running tests: pkg/raft/tracetest/... $(TESTFLAGS)"
	@go test $(TESTFLAGS) ./pkg/raft/tracetest/...

go-lint:
	@echo "  >  Running golangci-lint: $(PKG) $(LINTFLAGS)"
	@golangci-lint run $(LINTFLAGS) $(PKG)

go-get:
	@echo "  >  Checking if there is any missing dependencies..."
	@go get $(get)

.PHONY: go-update-deps
go-update-deps:
	@echo ">> updating Go dependencies"
	@for m in $$(go list -mod=readonly -m -f '{{ if and (not .Indirect) (not .Main)}}{{.Path}}{{end}}' all); do \
		go get $$m; \
	done
	@go mod tidy
ifneq (,$(wildcard vendor))
	@go mod vendor
endif

go-install:
	go install ./cmd/...

go-clean:
	@echo "  >  Cleaning build cache"
	@go clean

go-clean-test-cache:
	@echo "  >  Cleaning test cache"
	@go clean -testcache

.PHONY: test test-race test-stress lint go-test go-test-race go-test-stress-race go-lint verify-imports verify-race-scope

.PHONY: help
all: help
help: Makefile
	@echo
	@echo " Choose a command run in "$(PROJECTNAME)":"
	@echo
	@sed -n 's/^##//p' $< | column -t -s ':' |  sed -e 's/^/ /'
	@echo
