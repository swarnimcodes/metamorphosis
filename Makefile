# Everyday commands. Tools (golangci-lint, govulncheck) are pinned in tools.mod and run through `go tool`.
TOOL = go tool -modfile=tools.mod

.PHONY: run test race lint vuln acceptance compare check tidy

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

compare:     ## the working tree against what is deployed (github/main), byte for byte, on a copy of $$SOURCE_DB
	acceptance/compare/compare.sh

tidy:        ## go.mod must be tidy (tools.mod is maintained by `go get -modfile=tools.mod -tool`)
	go mod tidy

check: lint vuln race acceptance  ## what CI runs
