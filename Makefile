.PHONY: build run test test-race check spec
build:
	go build -o bin/td-backend ./cmd/td-backend
run:
	go run ./cmd/td-backend
test:
	go test ./...
test-race:
	go test -race ./...
check:
	go vet ./...
spec:
	python3 scripts/generate_openapi.py
	.venv/bin/python scripts/validate_openapi.py
	python3 scripts/validate_sqlite_design.py
