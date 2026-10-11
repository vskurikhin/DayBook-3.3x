# ---------------------------------------------------------------------------
# Local Docker configuration contract (ADR-005 / R3.4, TASK-002)
# ---------------------------------------------------------------------------
#
# config_parse strictly parses and validates the local configuration file
# ($(ENV_FILE), default docker/local.env) and leaves validated values in shell
# variables for the remainder of the recipe. It never sources/evaluates the
# file, never imports a secret value into Make, never echoes a value, and
# fails (nonzero) with a key-named, non-secret diagnostic on any grammar,
# allowlist, placeholder, precedence or range error.
#
# Precedence (R3.4): explicit non-secret Make command-line option > config file
# > documented defaults. Ambient environment variables are ignored for
# non-secret keys. The two required secrets are rejected if supplied on the
# Make command line or in the environment; they are read only from the file.
#
# Leaves shell variables: PREFIX TAG PLATFORM PROJECT DB_NAME DB_USER DB_PORT
# AUTH_PORT CORE_PORT API_PORT APP_PORT STARTUP_TIMEOUT LOCAL_DB_PASSWORD
# LOCAL_TOKEN_KEY.

define config_parse
$(if $(filter command line environment,$(origin LOCAL_DB_PASSWORD)),echo "error: forbidden: LOCAL_DB_PASSWORD must come only from the config file (not command line or environment)" >&2; exit 1;)\
$(if $(filter command line environment,$(origin LOCAL_TOKEN_KEY)),echo "error: forbidden: LOCAL_TOKEN_KEY must come only from the config file (not command line or environment)" >&2; exit 1;)\
export LC_ALL=C; \
cfg="$(ENV_FILE)"; \
[ -f "$$cfg" ] || { echo "error: configuration file not found: $$cfg" >&2; echo "  copy docker/local.env.example to docker/local.env (or set ENV_FILE=...)" >&2; exit 1; }; \
PREFIX=daybook-local; TAG=dev; PLATFORM=linux/arm64; PROJECT=daybook-local; \
DB_NAME=db; DB_USER=dbuser; \
DB_PORT=35432; AUTH_PORT=38089; CORE_PORT=38082; API_PORT=38081; APP_PORT=30080; \
STARTUP_TIMEOUT=180; \
LOCAL_DB_PASSWORD=""; LOCAL_TOKEN_KEY=""; \
seen=""; \
while IFS= read -r line || [ -n "$$line" ]; do \
  case "$$line" in ''|'#'*) continue ;; esac; \
  case "$$line" in *[!A-Za-z0-9_=./:-]*) echo "error: invalid character in configuration line (key '$${line%%=*}') in $$cfg" >&2; exit 1 ;; esac; \
  case "$$line" in *'='*) ;; *) echo "error: malformed line (missing '=') in $$cfg" >&2; exit 1 ;; esac; \
  key="$${line%%=*}"; val="$${line#*=}"; \
  case " $$seen " in *" $$key "*) echo "error: duplicate key '$$key' in $$cfg" >&2; exit 1 ;; esac; \
  seen="$$seen $$key"; \
  case "$$key" in \
    LOCAL_DB_PASSWORD) LOCAL_DB_PASSWORD="$$val" ;; \
    LOCAL_TOKEN_KEY) LOCAL_TOKEN_KEY="$$val" ;; \
    LOCAL_DB_NAME) DB_NAME="$$val" ;; \
    LOCAL_DB_USER) DB_USER="$$val" ;; \
    COMPOSE_PROJECT_NAME) PROJECT="$$val" ;; \
    IMAGE_PREFIX) PREFIX="$$val" ;; \
    IMAGE_TAG) TAG="$$val" ;; \
    DOCKER_PLATFORM) PLATFORM="$$val" ;; \
    DB_PORT) DB_PORT="$$val" ;; \
    AUTH_PORT) AUTH_PORT="$$val" ;; \
    CORE_PORT) CORE_PORT="$$val" ;; \
    API_PORT) API_PORT="$$val" ;; \
    APP_PORT) APP_PORT="$$val" ;; \
    STARTUP_TIMEOUT) STARTUP_TIMEOUT="$$val" ;; \
    *) echo "error: unknown key '$$key' in $$cfg" >&2; exit 1 ;; \
  esac; \
done < "$$cfg"; \
$(if $(filter command line,$(origin IMAGE_PREFIX)),PREFIX="$(IMAGE_PREFIX)";)\
$(if $(filter command line,$(origin IMAGE_TAG)),TAG="$(IMAGE_TAG)";)\
$(if $(filter command line,$(origin DOCKER_PLATFORM)),PLATFORM="$(DOCKER_PLATFORM)";)\
$(if $(filter command line,$(origin COMPOSE_PROJECT_NAME)),PROJECT="$(COMPOSE_PROJECT_NAME)";)\
$(if $(filter command line,$(origin LOCAL_DB_NAME)),DB_NAME="$(LOCAL_DB_NAME)";)\
$(if $(filter command line,$(origin LOCAL_DB_USER)),DB_USER="$(LOCAL_DB_USER)";)\
$(if $(filter command line,$(origin DB_PORT)),DB_PORT="$(DB_PORT)";)\
$(if $(filter command line,$(origin AUTH_PORT)),AUTH_PORT="$(AUTH_PORT)";)\
$(if $(filter command line,$(origin CORE_PORT)),CORE_PORT="$(CORE_PORT)";)\
$(if $(filter command line,$(origin API_PORT)),API_PORT="$(API_PORT)";)\
$(if $(filter command line,$(origin APP_PORT)),APP_PORT="$(APP_PORT)";)\
$(if $(filter command line,$(origin STARTUP_TIMEOUT)),STARTUP_TIMEOUT="$(STARTUP_TIMEOUT)";)\
[ -n "$$LOCAL_DB_PASSWORD" ] || { echo "error: missing required secret LOCAL_DB_PASSWORD in $$cfg" >&2; exit 1; }; \
[ -n "$$LOCAL_TOKEN_KEY" ] || { echo "error: missing required secret LOCAL_TOKEN_KEY in $$cfg" >&2; exit 1; }; \
case "$$LOCAL_DB_PASSWORD" in *change-me*) echo "error: LOCAL_DB_PASSWORD contains the shipped placeholder; replace it with a generated secret" >&2; exit 1 ;; esac; \
case "$$LOCAL_TOKEN_KEY" in *change-me*) echo "error: LOCAL_TOKEN_KEY contains the shipped placeholder; replace it with a generated secret" >&2; exit 1 ;; esac; \
[ "$${#LOCAL_DB_PASSWORD}" -ge 8 ] && [ "$${#LOCAL_DB_PASSWORD}" -le 128 ] || { echo "error: LOCAL_DB_PASSWORD length must be 8-128 characters" >&2; exit 1; }; \
case "$$LOCAL_DB_PASSWORD" in *[!A-Za-z0-9_-]*) echo "error: LOCAL_DB_PASSWORD contains characters outside [A-Za-z0-9_-]" >&2; exit 1 ;; esac; \
[ "$${#LOCAL_TOKEN_KEY}" -ge 32 ] && [ "$${#LOCAL_TOKEN_KEY}" -le 128 ] || { echo "error: LOCAL_TOKEN_KEY length must be 32-128 characters" >&2; exit 1; }; \
case "$$LOCAL_TOKEN_KEY" in *[!A-Za-z0-9_-]*) echo "error: LOCAL_TOKEN_KEY contains characters outside [A-Za-z0-9_-]" >&2; exit 1 ;; esac; \
case "$$DB_NAME" in [a-z]*) ;; *) echo "error: LOCAL_DB_NAME must begin with a lowercase letter" >&2; exit 1 ;; esac; \
case "$$DB_NAME" in *[!a-z0-9_]*) echo "error: LOCAL_DB_NAME contains characters outside [a-z0-9_]" >&2; exit 1 ;; esac; \
[ "$${#DB_NAME}" -le 63 ] || { echo "error: LOCAL_DB_NAME exceeds 63 characters" >&2; exit 1; }; \
case "$$DB_USER" in [a-z]*) ;; *) echo "error: LOCAL_DB_USER must begin with a lowercase letter" >&2; exit 1 ;; esac; \
case "$$DB_USER" in *[!a-z0-9_]*) echo "error: LOCAL_DB_USER contains characters outside [a-z0-9_]" >&2; exit 1 ;; esac; \
[ "$${#DB_USER}" -le 63 ] || { echo "error: LOCAL_DB_USER exceeds 63 characters" >&2; exit 1; }; \
case "$$PROJECT" in [a-z0-9]*) ;; *) echo "error: COMPOSE_PROJECT_NAME must begin with an alphanumeric character" >&2; exit 1 ;; esac; \
case "$$PROJECT" in *[!a-z0-9-]*) echo "error: COMPOSE_PROJECT_NAME contains characters outside [a-z0-9-]" >&2; exit 1 ;; esac; \
case "$$PREFIX" in [a-z0-9]*) ;; *) echo "error: IMAGE_PREFIX must begin with an alphanumeric character" >&2; exit 1 ;; esac; \
case "$$PREFIX" in *[!a-z0-9._/-]*) echo "error: IMAGE_PREFIX contains characters outside [a-z0-9._/-]" >&2; exit 1 ;; esac; \
[ -n "$$TAG" ] || { echo "error: IMAGE_TAG must not be empty" >&2; exit 1; }; \
case "$$TAG" in *[!a-zA-Z0-9_.-]*) echo "error: IMAGE_TAG contains characters outside [a-zA-Z0-9_.-]" >&2; exit 1 ;; esac; \
[ "$${#TAG}" -le 128 ] || { echo "error: IMAGE_TAG exceeds 128 characters" >&2; exit 1; }; \
[ "$$PLATFORM" = "linux/arm64" ] || { echo "error: unsupported DOCKER_PLATFORM '$$PLATFORM'; only linux/arm64 is supported on native ARM64" >&2; exit 1; }; \
case "$$DB_PORT" in ''|*[!0-9]*) echo "error: DB_PORT must be an integer 1-65535" >&2; exit 1 ;; esac; \
[ "$$DB_PORT" -ge 1 ] && [ "$$DB_PORT" -le 65535 ] || { echo "error: DB_PORT out of range 1-65535" >&2; exit 1; }; \
case "$$AUTH_PORT" in ''|*[!0-9]*) echo "error: AUTH_PORT must be an integer 1-65535" >&2; exit 1 ;; esac; \
[ "$$AUTH_PORT" -ge 1 ] && [ "$$AUTH_PORT" -le 65535 ] || { echo "error: AUTH_PORT out of range 1-65535" >&2; exit 1; }; \
case "$$CORE_PORT" in ''|*[!0-9]*) echo "error: CORE_PORT must be an integer 1-65535" >&2; exit 1 ;; esac; \
[ "$$CORE_PORT" -ge 1 ] && [ "$$CORE_PORT" -le 65535 ] || { echo "error: CORE_PORT out of range 1-65535" >&2; exit 1; }; \
case "$$API_PORT" in ''|*[!0-9]*) echo "error: API_PORT must be an integer 1-65535" >&2; exit 1 ;; esac; \
[ "$$API_PORT" -ge 1 ] && [ "$$API_PORT" -le 65535 ] || { echo "error: API_PORT out of range 1-65535" >&2; exit 1; }; \
case "$$APP_PORT" in ''|*[!0-9]*) echo "error: APP_PORT must be an integer 1-65535" >&2; exit 1 ;; esac; \
[ "$$APP_PORT" -ge 1 ] && [ "$$APP_PORT" -le 65535 ] || { echo "error: APP_PORT out of range 1-65535" >&2; exit 1; }; \
[ "$$DB_PORT" != "$$AUTH_PORT" ] && [ "$$DB_PORT" != "$$CORE_PORT" ] && [ "$$DB_PORT" != "$$API_PORT" ] && [ "$$DB_PORT" != "$$APP_PORT" ] && [ "$$AUTH_PORT" != "$$CORE_PORT" ] && [ "$$AUTH_PORT" != "$$API_PORT" ] && [ "$$AUTH_PORT" != "$$APP_PORT" ] && [ "$$CORE_PORT" != "$$API_PORT" ] && [ "$$CORE_PORT" != "$$APP_PORT" ] && [ "$$API_PORT" != "$$APP_PORT" ] || { echo "error: service ports must be distinct" >&2; exit 1; }; \
case "$$STARTUP_TIMEOUT" in ''|*[!0-9]*) echo "error: STARTUP_TIMEOUT must be an integer" >&2; exit 1 ;; esac; \
[ "$$STARTUP_TIMEOUT" -ge 1 ] && [ "$$STARTUP_TIMEOUT" -le 3600 ] || { echo "error: STARTUP_TIMEOUT must be 1-3600 seconds" >&2; exit 1; }
endef

# Write private runtime files. Callers must guard running services before
# replacing mounted files. The two final renames are not a pair transaction.
# Template secrets travel via a private sed script, never process arguments.
define config_publish
( \
  umask 077; \
  runtimedir="$(WORKFLOW)/$$PROJECT/runtime"; \
  mkdir -p "$$runtimedir" && chmod 700 "$$runtimedir" || exit 1; \
  tmpdir="$$runtimedir/.tmp.$$$$"; mkdir "$$tmpdir" && chmod 700 "$$tmpdir" || exit 1; \
  trap 'rm -rf "$$tmpdir" 2>/dev/null || true' EXIT HUP INT TERM; \
  corekey="$$(printf '%s' $$LOCAL_TOKEN_KEY | base64 | tr -d '\n')"; \
  { \
    printf 'LOCAL_DB_PASSWORD=%s\n' "$$LOCAL_DB_PASSWORD"; \
    printf 'LOCAL_TOKEN_KEY=%s\n' "$$LOCAL_TOKEN_KEY"; \
    printf 'LOCAL_DB_NAME=%s\n' "$$DB_NAME"; \
    printf 'LOCAL_DB_USER=%s\n' "$$DB_USER"; \
    printf 'COMPOSE_PROJECT_NAME=%s\n' "$$PROJECT"; \
    printf 'IMAGE_PREFIX=%s\n' "$$PREFIX"; \
    printf 'IMAGE_TAG=%s\n' "$$TAG"; \
    printf 'DOCKER_PLATFORM=%s\n' "$$PLATFORM"; \
    printf 'DB_PORT=%s\n' "$$DB_PORT"; \
    printf 'AUTH_PORT=%s\n' "$$AUTH_PORT"; \
    printf 'CORE_PORT=%s\n' "$$CORE_PORT"; \
    printf 'API_PORT=%s\n' "$$API_PORT"; \
    printf 'APP_PORT=%s\n' "$$APP_PORT"; \
    printf 'STARTUP_TIMEOUT=%s\n' "$$STARTUP_TIMEOUT"; \
    printf 'CORE_SERVER_TOKEN_SIGNING_KEY=%s\n' "$$corekey"; \
    printf 'DAYBOOK_AUTH_CONFIG=%s\n' "$$runtimedir/auth-server.yaml"; \
  } > "$$tmpdir/env" || exit 1; \
  chmod 600 "$$tmpdir/env" || exit 1; \
  { \
    printf 's/"dbname": ".*"/"dbname": "%s"/\n' "$$DB_NAME"; \
    printf 's/"dbuser": ".*"/"dbuser": "%s"/\n' "$$DB_USER"; \
    printf 's/"dbpassword": ".*"/"dbpassword": "%s"/\n' "$$LOCAL_DB_PASSWORD"; \
  } > "$$tmpdir/template.sed" || exit 1; \
  sed -f "$$tmpdir/template.sed" "$(ROOT)/docker/local-auth-config.json" > "$$tmpdir/auth-server.yaml" || exit 1; \
  rm -f "$$tmpdir/template.sed" || exit 1; \
  chmod 600 "$$tmpdir/auth-server.yaml" || exit 1; \
  [ -z "$$VERIFY_PUBLISH_FAIL" ] || exit 1; \
  mv -f "$$tmpdir/env" "$$runtimedir/env" || exit 1; \
  mv -f "$$tmpdir/auth-server.yaml" "$$runtimedir/auth-server.yaml" || exit 1; \
  cp "$$runtimedir/auth-server.yaml" "docker/local.auth-server.yaml" || exit 1; \
  rmdir "$$tmpdir" 2>/dev/null || true \
)
endef

# ---------------------------------------------------------------------------
# Configuration and image verification seams (TASK-002)
# ---------------------------------------------------------------------------

# Runs config_parse only; echoes CONFIG_OK on success. Used to assert that
# invalid inputs are rejected without echoing any value.
.PHONY: .config-parse-run
.config-parse-run:
	@$(config_parse) && echo 'CONFIG_OK'

# Runs config_parse and prints the derived padded Base64 core key (no newline),
# for byte-mapping assertions.
.PHONY: .config-corekey-seam
.config-corekey-seam:
	@$(config_parse) && printf 'CORE=%s\n' $$(printf '%s' $$LOCAL_TOKEN_KEY | base64 | tr -d '\n')

# Runs config_parse + config_publish and prints the runtime snapshot path.
.PHONY: .config-publish-seam
.config-publish-seam:
	@$(config_parse) && $(config_publish) && echo "RUNTIME=$(WORKFLOW)/$$PROJECT/runtime"
