# Everyday commands. Tools (golangci-lint, govulncheck) are pinned in tools.mod and run through `go tool`.
TOOL = go tool -modfile=tools.mod

.PHONY: run test race lint vuln acceptance compare check tidy migration migrations-sum generate

run:         ## run the API (reads .env)
	go run ./cmd/api

test:        ## unit tests (need Postgres 18; TEST_DATABASE_URL defaults to postgres://postgres@localhost/postgres)
	go test ./internal/... -count=1

race:        ## unit tests with the race detector
	go test ./internal/... -count=1 -race

lint:
	$(TOOL) golangci-lint run ./...

vuln:        ## known vulnerabilities in the dependencies the code actually calls
	$(TOOL) govulncheck ./...

acceptance:  ## builds and starts the server on a throwaway database and drives it over HTTP
	acceptance/run.sh

compare:     ## the working tree against what is deployed (github/main), byte for byte, on a copy of $$SOURCE_DB (COMPARE_NUMBERS=value ignores number formatting, 90 vs 90.0)
	acceptance/compare/compare.sh

generate:    ## sqlc (queries/ -> internal/db) and oapi-codegen (api/openapi.yaml -> internal/httpapi/gen); commit the output
	$(TOOL) sqlc generate
	$(TOOL) oapi-codegen -config api/oapi-codegen.yaml api/openapi.yaml

migration:   ## new migration: make migration name=add_something
	@test -n "$(name)" || { echo "usage: make migration name=add_something"; exit 1; }
	$(TOOL) goose -dir migrations create $(name) sql
	@echo "Edit it, then run: make migrations-sum"

migrations-sum:  ## record the hash of a NEW migration file; never run it to bless an edit to an applied one
	cd migrations && sha256sum *.sql > migrations.sum

tidy:        ## go.mod must be tidy (tools.mod is maintained by `go get -modfile=tools.mod -tool`)
	go mod tidy

check: lint vuln race acceptance  ## what CI runs
