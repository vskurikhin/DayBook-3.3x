define docker_compose
$(call check_tool,docker-compose) && ( \
cd docker && \
docker-compose \
  --env-file "$(WORKFLOW)/$$PROJECT/runtime/env" \
  --project-name "$$PROJECT" \
  -f compose.local.yaml \
  $(1) $(2); \
cd - )
endef

define docker_compose_d
$(call check_tool,docker-compose) && ( \
cd docker && \
docker-compose \
  --env-file "$(WORKFLOW)/$$PROJECT/runtime/env" \
  --project-name "$$PROJECT" \
  -f compose.local.yaml \
  $(1) -d $(2); \
cd - )
endef

define docker_compose_down
$(call check_tool,docker-compose) && ( \
cd docker && \
docker-compose \
  --env-file "$(WORKFLOW)/$$PROJECT/runtime/env" \
  --project-name "$$PROJECT" \
  -f compose.local.yaml \
  down $(1) $(2); \
cd - )
endef

# docker_preflight: verify a local Docker endpoint and an ARM64 engine, and
# confirm the validated platform is the supported linux/arm64. Uses the PLATFORM
# shell variable set by config_parse. Fails before any build on an unsupported
# platform or a remote/unknown Docker endpoint.
define docker_preflight
$(call check_tool,docker) && \
case "$${DOCKER_HOST:-}" in tcp://*|http://*|ssh://*) echo "error: remote DOCKER_HOST ($$DOCKER_HOST) is not supported; use a local Docker endpoint (host JDBC/psql target loopback)" >&2; exit 1 ;; esac; \
eng_arch="$$(docker info --format '{{.Architecture}}' 2>/dev/null)"; \
case "$$eng_arch" in arm64|aarch64) ;; *) echo "error: Docker engine architecture '$$eng_arch' is not ARM64; only linux/arm64 is supported (DOCKER_PLATFORM=$$PLATFORM)" >&2; exit 1 ;; esac; \
case "$$PLATFORM" in linux/arm64) ;; *) echo "error: unsupported DOCKER_PLATFORM '$$PLATFORM'; only linux/arm64 is supported" >&2; exit 1 ;; esac
endef
