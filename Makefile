.PHONY: run run-licensed dev-license test lint build ai-test docker smoke smoke-unlicensed

# Development license (.dev/, ignored by git): a local key trusted only by run-licensed builds.
DEV ?= .dev
EDITION ?= mall
TIER ?= enterprise
ADD ?= substation
BUILD_KEY = -X github.com/DarmawanWeb/netra-fleet-be/internal/license.BuildKey=$$(cat $(DEV)/dev.pub)

run:            ## run the API against a local PostgreSQL (no license: core only, one robot)
	go run ./cmd/netra-api

dev-license:    ## a development key and license in .dev/ (EDITION, TIER, ADD to choose the modules)
	@test -f $(DEV)/dev.key || go run ./cmd/netra-license keygen -out $(DEV)/dev
	go run ./cmd/netra-license issue -key $(DEV)/dev.key -client Development -edition $(EDITION) \
		-tier $(TIER) -add "$(ADD)" -days 30 -name "NETRA Dev" -out $(DEV)/dev.lic

run-licensed: dev-license  ## run the API with the development license
	NETRA_LICENSE_FILE=$(DEV)/dev.lic go run -ldflags "$(BUILD_KEY)" ./cmd/netra-api

test:           ## unit tests
	go test ./...

lint:           ## go vet + golangci-lint + ruff
	go vet ./...
	golangci-lint run
	cd ai && ruff check . && ruff format --check .

ai-test:
	cd ai && python -m pytest -q

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/netra-api ./cmd/netra-api

docker:
	docker build -t netra-api .
	docker build -t netra-ai ai

smoke:          ## end-to-end check against a server from run-licensed (NETRA_URL, NETRA_PASSWORD)
	python3 scripts/smoke_test.py

smoke-unlicensed: ## checks against a server from run (core only)
	python3 scripts/smoke_unlicensed.py
