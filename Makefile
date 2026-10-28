SHELL := /bin/bash
GO ?= go
NPM ?= npm
COMPOSE := docker compose -f deploy/local/compose.yaml
GO_ENV := GOMODCACHE=/tmp/hc-go-mod GOCACHE=/tmp/hc-go-build
RUN_ENV = HC_DB_DSN_FILE=$(CURDIR)/.local/$(ROLE)-dsn HC_REDIS_ADDR=127.0.0.1:56379 HC_REDIS_TLS=false HC_REDIS_PASSWORD_FILE=$(CURDIR)/.local/redis-password
ROLE ?= api
ACCESS_ENV = $(if $(wildcard .local/access-config.json),HC_ACCESS_CONFIG_FILE=$(CURDIR)/.local/access-config.json HC_ACCESS_CA_FILE=$(CURDIR)/.local/auth/cert.pem,)
CONTEXT_ENV = $(if $(wildcard .local/context/endpoints.local.json),HC_CONTEXT_ENDPOINTS_FILE=$(CURDIR)/.local/context/endpoints.local.json,)
CONNECTOR_ENV = $(if $(wildcard .local/connectors/endpoints.json),HC_CONNECTOR_ENDPOINTS_FILE=$(CURDIR)/.local/connectors/endpoints.json,)
SKILL_SOURCE_ENV = $(if $(wildcard .local/skill-source-proxy.json),HC_SKILL_SOURCE_PROXY_FILE=$(CURDIR)/.local/skill-source-proxy.json,)
AUTH_ENV = HC_PUBLIC_URL=$(if $(wildcard .local/access-config.json),https://portal.harness.localhost:42013,https://127.0.0.1:42013) HC_OIDC_ISSUER=$(if $(wildcard .local/access-config.json),https://portal.harness.localhost:42013/oidc,https://127.0.0.1:42013/oidc) HC_OIDC_CLIENT_ID=harness-cloud HC_OIDC_CLIENT_SECRET_FILE=$(CURDIR)/.local/auth/client-secret HC_ORGANIZATION_ID=$$(cat .local/auth/organization-id) HC_ENVIRONMENT=local HC_DATA_KEY_FILE=$(CURDIR)/.local/auth/data-key HC_DATA_KEY_ID=local-v1 HC_OIDC_SIGNING_KEY_FILE=$(CURDIR)/.local/auth/signing-key.pem HC_MODEL_ALLOWLIST_FILE=$(CURDIR)/.local/auth/model-allowlist.json

.PHONY: dev-init infra-up infra-stop migrate run portal test test-integration check build ci auth-init auth-apply run-auth runtime-build runtime-test runtime-model-test storage-build storage-test network-build network-test run-worker worker-runtime-test worker-init access-init run-gateway gateway-browser-test
.PHONY: workspace-init workspace-template workspace-test runtime-approve
.PHONY: local-bootstrap
local-bootstrap: migrate
	$(GO_ENV) HC_DB_DSN_FILE=$(CURDIR)/.local/migrator-dsn $(AUTH_ENV) HC_IDENTITY_ACCOUNTS_FILE=$(CURDIR)/.local/auth/bootstrap-accounts.json HC_OPERATOR_REF=local-development $(GO) run ./services/cmd/hcctl dev-bootstrap

run-auth run-worker run-gateway run-model-gateway: local-bootstrap

runtime-approve:
	$(GO_ENV) HC_DB_DSN_FILE=$(CURDIR)/.local/migrator-dsn HC_OPERATOR_REF=local-operator HC_RUNTIME_MANIFEST_FILE=$(CURDIR)/.local/runtime-manifest.json $(GO) run ./services/cmd/hcctl dev-runtime-approve

runtime-model-test:
	test -n "$$HC_REAL_MODEL_KEY_FILE"
	HC_RUNTIME_IMAGE=hc-dsh-runtime:0.1.0 node --test infrastructure/runtime/tests/real-model-integration.test.mjs

workspace-template:
	$(GO_ENV) HC_DB_DSN_FILE=$(CURDIR)/.local/migrator-dsn HC_OPERATOR_REF=local-operator $(GO) run ./services/cmd/hcctl workspace-template

workspace-init:
	python3 infrastructure/terraform/setup-local.py
	$(GO_ENV) HC_DB_DSN_FILE=$(CURDIR)/.local/migrator-dsn HC_OPERATOR_REF=local-operator $(GO) run ./services/cmd/hcctl workspace-init

storage-build:
	docker build --tag hc-storage-helper:0.1.0 infrastructure/storage

network-build:
	docker build --tag hc-network-helper:0.1.0 infrastructure/network

storage-test:
	HC_STORAGE_IMAGE=hc-storage-helper:0.1.0 HC_RUNTIME_IMAGE=hc-dsh-runtime:0.1.0 node --test infrastructure/storage/docker-integration.test.mjs

network-test:
	HC_NETWORK_IMAGE=hc-network-helper:0.1.0 HC_RUNTIME_IMAGE=hc-dsh-runtime:0.1.0 node --test infrastructure/network/docker-integration.test.mjs

workspace-test:
	HC_TEST_DB_DSN_FILE=$(CURDIR)/.local/migrator-dsn HC_TEST_TERRAFORM_OPTIONS_FILE=$(CURDIR)/.local/workspace-engine/options.json $(GO_ENV) $(GO) test -race -count=1 -v ./services/internal/workspace

runtime-test:
	HC_RUNTIME_IMAGE=hc-dsh-runtime:0.1.0 node --test infrastructure/runtime/tests/docker-integration.test.mjs infrastructure/runtime/tests/install-tools-integration.test.mjs

runtime-build:
	docker build --tag hc-dsh-runtime:0.1.0 infrastructure/runtime

auth-init:
	$(GO_ENV) $(GO) run ./services/cmd/hcctl dev-auth-init

auth-apply:
	$(GO_ENV) HC_DB_DSN_FILE=$(CURDIR)/.local/migrator-dsn $(AUTH_ENV) HC_IDENTITY_ACCOUNTS_FILE=$(CURDIR)/.local/auth/bootstrap-accounts.json HC_OPERATOR_REF=local-development $(GO) run ./services/cmd/hcctl identity-import

worker-init:
	$(GO_ENV) $(GO) run ./services/cmd/hcctl dev-worker-init

access-init:
	$(GO_ENV) HC_DB_DSN_FILE=$(CURDIR)/.local/migrator-dsn $(GO) run ./services/cmd/hcctl dev-access-init

run-gateway:
	$(GO_ENV) HC_DB_DSN_FILE=$(CURDIR)/.local/gateway-dsn HC_REDIS_ADDR=127.0.0.1:56379 HC_REDIS_TLS=false HC_REDIS_PASSWORD_FILE=$(CURDIR)/.local/redis-password HC_ACCESS_CONFIG_FILE=$(CURDIR)/.local/access-config.json $(GO) run ./services/cmd/gateway

run-worker:
	$(GO_ENV) HC_DB_DSN_FILE=$(CURDIR)/.local/worker-dsn HC_REDIS_ADDR=127.0.0.1:56379 HC_REDIS_TLS=false HC_REDIS_PASSWORD_FILE=$(CURDIR)/.local/redis-password HC_WORKER_CONFIG_FILE=$(CURDIR)/.local/worker-config.json $(CONTEXT_ENV) $(CONNECTOR_ENV) $(GO) run ./services/cmd/worker

worker-runtime-test:
	test -n "$$HC_TEST_DB_DSN_FILE"
	HC_TEST_TERRAFORM_OPTIONS_FILE=$(CURDIR)/.local/workspace-engine/options.json HC_WORKER_CONFIG_FILE="$(if $(HC_WORKER_CONFIG_FILE),$(abspath $(HC_WORKER_CONFIG_FILE)),$(CURDIR)/.local/worker-config.json)" $(GO_ENV) $(GO) test -race -count=1 -v ./services/internal/worker -run '^(TestLocalHostUsesEmbeddedWorkspaceAndOriginalHardCapacityAsset|TestDurableExecutorRunsEmbeddedLifecycleAndRetainsData)$$'

gateway-browser-test:
	test -n "$$HC_TEST_DB_DSN_FILE"
	HC_TEST_DB_DSN_FILE="$(abspath $(HC_TEST_DB_DSN_FILE))" HC_WORKER_CONFIG_FILE=$(CURDIR)/.local/worker-config.json HC_GATEWAY_BROWSER=true HC_TEST_TERRAFORM_OPTIONS_FILE=$(CURDIR)/.local/workspace-engine/options.json $(GO_ENV) $(GO) test -race -count=1 -v ./services/internal/worker -run '^TestDurableExecutorRunsEmbeddedLifecycleAndRetainsData$$'

run-auth:
	$(GO_ENV) $(RUN_ENV) $(AUTH_ENV) $(ACCESS_ENV) $(CONTEXT_ENV) $(SKILL_SOURCE_ENV) $(CONNECTOR_ENV) $(GO) run ./services/cmd/api
dev-init:
	$(GO_ENV) $(GO) run ./services/cmd/hcctl dev-init

infra-up:
	HC_LOCAL_UID=$$(id -u) HC_LOCAL_GID=$$(id -g) $(COMPOSE) up -d --wait

infra-stop:
	$(COMPOSE) stop

migrate:
	$(GO_ENV) HC_DB_DSN_FILE=$(CURDIR)/.local/migrator-dsn $(GO) run ./services/cmd/hcctl migrate
	$(GO_ENV) HC_DB_DSN_FILE=$(CURDIR)/.local/migrator-dsn $(GO) run ./services/cmd/hcctl dev-grants

run:
	$(GO_ENV) $(RUN_ENV) $(GO) run ./services/cmd/$(ROLE)

portal:
	cd apps && $(NPM) run dev -- --host 127.0.0.1

.PHONY: portal-preview
portal-preview:
	cd apps && $(NPM) run preview

test:
	python3 -m unittest discover -s services/internal/worker -p '*_test.py'
	$(GO_ENV) $(GO) test -race ./...
	cd apps && $(NPM) test
	cd infrastructure/runtime && node --test tests/*.test.mjs
	python3 -m unittest discover -s infrastructure/storage -p 'test_*.py'
	python3 -m unittest discover -s infrastructure/network -p 'test_*.py'

test-integration:
	test -n "$$HC_TEST_DB_DSN$$HC_TEST_DB_DSN_FILE"
	$(GO_ENV) HC_TEST_DB_DSN_FILE="$(if $(HC_TEST_DB_DSN_FILE),$(abspath $(HC_TEST_DB_DSN_FILE)),)" $(GO) test -race -count=1 -v ./db ./services/cmd/hcctl ./services/internal/devlocal ./services/internal/identity ./services/internal/settings ./services/internal/skills ./services/internal/contextservice ./services/internal/connectors ./services/internal/instances ./services/internal/access ./services/internal/admission ./services/internal/runtimeapproval ./services/internal/operations ./services/internal/worker

check:
	set -e; formatting=$$(gofmt -l db services); test -z "$$formatting"
	$(GO_ENV) $(GO) vet ./...
	cd apps && $(NPM) run typecheck

build:
	$(GO_ENV) $(GO) build ./services/cmd/...
	cd apps && $(NPM) run build

ci: check test build

.PHONY: test-recovery
test-recovery:
	test -n "$$HC_TEST_DB_DSN_FILE"
	test -n "$$HC_WORKER_CONFIG_FILE"
	$(GO_ENV) $(GO) test -race -count=1 -v ./services/internal/operations

.PHONY: test-recovery-ledger
test-recovery-ledger:
	test -n "$$HC_TEST_DB_DSN_FILE"
	$(GO_ENV) $(GO) test -race -count=1 ./db ./services/internal/operations ./services/internal/instances ./services/internal/settings ./services/cmd/hcctl

.PHONY: test-recovery-source-fence
test-recovery-source-fence:
	test -n "$$HC_TEST_DB_DSN_FILE"
	test -n "$$HC_TEST_API_DSN_FILE"
	$(GO_ENV) HC_TEST_DB_DSN_FILE="$(abspath $(HC_TEST_DB_DSN_FILE))" HC_TEST_API_DSN_FILE="$(abspath $(HC_TEST_API_DSN_FILE))" $(GO) test -race -count=1 -v ./services/internal/operations -run 'TestRecoveryInventoryOnIsolatedPostgres|TestDeletedRetainedDisk'
	$(GO_ENV) $(GO) test -race -count=1 ./services/cmd/hcctl

.PHONY: test-product-restore local-backup-timer
local-backup-timer:
	python3 infrastructure/local/install-backup-timer.py

test-product-restore:
	test -n "$$HC_TEST_DB_DSN_FILE"
	test -n "$$HC_WORKER_CONFIG_FILE"
	test -n "$$HC_PRODUCT_FILE_PROOF_FILE"
	test -n "$$HC_PRODUCT_HISTORY_PROOF_FILE"
	HC_PRODUCT_STORAGE_RESTORE=true $(GO_ENV) $(GO) test -race -count=1 -v ./services/internal/operations -run '^TestRealProductNativeHistoryFromEncryptedIndependentAssetRestore$$'

.PHONY: model-gateway-init run-model-gateway
model-gateway-init:
	python3 infrastructure/model-gateway/setup-local.py

run-model-gateway:
	$(GO_ENV) HC_DB_DSN_FILE=$(CURDIR)/.local/model_gateway-dsn HC_MODEL_GATEWAY_CONFIG_FILE=$(CURDIR)/.local/model-gateway/config.json $(GO) run ./services/cmd/model-gateway

.PHONY: context-init context-up context-test
context-init:
	python3 deploy/context/prepare.py

context-up: context-init
	docker compose --env-file .local/context/compose.env -f deploy/context/compose.yaml --profile knowledge --profile memory up -d --build

context-test:
	python3 deploy/context/test_services.py
	docker compose --env-file .local/context/compose.env -f deploy/context/compose.yaml run --rm --no-deps bridge python test_recovery.py
