.PHONY: run test lint build ai-test docker smoke

run:            ## run the API against a local PostgreSQL
	go run ./cmd/netra-api

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

smoke:          ## end-to-end check against a running server (NETRA_URL, NETRA_PASSWORD)
	python3 scripts/smoke_test.py
