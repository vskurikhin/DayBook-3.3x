# Image staging fragments (ADR-002 / R3.3). Each focused fragment builds only
# its own component's inputs and real Gradle dependencies into an isolated,
# operation-owned context under .daybook-workflow/images/<component>, then
# produces the exact Compose tag <PREFIX>/<component>:<TAG>. PREFIX, TAG and
# PLATFORM are shell variables set by config_parse.

define image_auth_frag
echo "==> image-auth: building $$PREFIX/auth:$$TAG ($$PLATFORM)"; \
staging="$(ROOT)/auth"; \
docker build --platform "$$PLATFORM" -t "$$PREFIX/auth:$$TAG" -f "$$staging/Dockerfile" "$$staging" && \
mkdir -p .daybook-workflow/images/ && \
docker image inspect "$$PREFIX/auth:$$TAG" > .daybook-workflow/images/Manifest-auth.json
endef

define image_core_frag
echo "==> image-core: building $$PREFIX/core:$$TAG ($$PLATFORM)"; \
staging="$(ROOT)/core"; \
docker build --platform "$$PLATFORM" -t "$$PREFIX/core:$$TAG" -f "$(ROOT)/core/src/main/resources/Dockerfile" "$$staging" && \
mkdir -p .daybook-workflow/images/ && \
docker image inspect "$$PREFIX/core:$$TAG" > .daybook-workflow/images/Manifest-core.json
endef

define image_api_frag
echo "==> image-api: building $$PREFIX/api:$$TAG ($$PLATFORM)"; \
( cd "$(ROOT)" && \
  ./gradlew :api:build -x test \
  		-Dquarkus.native.enabled=true \
  		-Dquarkus.package.jar.enabled=false \
  		-Dquarkus.native.reuse-existing=true \
  		-Dquarkus.native.container-build=true \
  		-Dquarkus.native.container-runtime=docker \
  		-Dquarkus.native.builder-image=quay.io/quarkus/ubi9-quarkus-mandrel-builder-image:jdk-21 \
  		-Dquarkus.container-image.build=true \
  		-Dquarkus.container-image.builder=docker \
  		-Dquarkus.container-image.image="$$PREFIX/api:$$TAG" \
  		-Dquarkus.container-image.push=false && \
  mkdir -p .daybook-workflow/images/ && \
  docker image inspect "$$PREFIX/api:$$TAG" > .daybook-workflow/images/Manifest-api.json )
endef

define image_app_frag
echo "==> image-app: building $$PREFIX/app:$$TAG ($$PLATFORM)"; \
[ -d "$(ROOT)/app/dist" ] || { echo "error: app/dist missing after npm run build" >&2; exit 1; }; \
[ -d "$(ROOT)/docker/nginx" ] || { echo "error: docker/nginx missing after npm run build" >&2; exit 1; }; \
staging="$(ROOT)/docker/nginx"; \
cd "$$staging" && \
rm -rf "$$staging/html"; mkdir -p "$$staging/html" && \
cp -R "$(ROOT)/app/dist/." "$$staging/html/"; \
cd - && \
docker build --platform "$$PLATFORM" -t "$$PREFIX/app:$$TAG" -f "$$staging/Dockerfile" "$$staging" && \
mkdir -p .daybook-workflow/images/ && \
docker image inspect "$$PREFIX/app:$$TAG" > .daybook-workflow/images/Manifest-app.json
endef
