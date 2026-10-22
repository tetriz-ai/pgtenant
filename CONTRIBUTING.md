# Contributing

Thank you for helping. The scope is small on purpose. Open an issue before starting a new entry
point or changing what the library writes to Postgres, so we agree on it first.

## Run the checks

The suite runs every entry point against a real Postgres, including tests that one tenant can
neither read nor write another's rows. These are the same checks CI runs. The tests need the
compose Postgres:

```sh
docker compose up -d   # or: podman-compose up -d
go build ./...
go vet ./...
PGTENANT_REQUIRE_TEST_DB=1 go test -race -count=1 ./...
GOTOOLCHAIN=go1.26.0 golangci-lint run   # v2.11.4, the version CI pins; configuration in .golangci.yml
scripts/run-examples.sh
buf lint            # after editing a .proto file; then buf generate
GOTOOLCHAIN=go1.27.3 go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
```

Install the pinned linter, buf, and the code generators `buf generate` uses:

```sh
GOTOOLCHAIN=go1.26.0 go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1
go install github.com/bufbuild/buf/cmd/buf@v1.67.0
```

Go 1.26 is the floor. The linter is built and run with Go 1.26, as the Lint CI job does.
golangci-lint v2.11 panics on Go 1.27 code, so both commands name the toolchain rather than
take the one installed.

With `PGTENANT_REQUIRE_TEST_DB=1` the tests wait up to 30 seconds for the compose Postgres to
accept connections. If a Postgres already listens on 5433, reuse it, or set `PGTENANT_TEST_HOST`
and `PGTENANT_TEST_PORT`.

## Every change needs a test

A pull request without a test is not merged. Tests are table-driven, like the existing
files. A change a user can see, such as a setting pgtenant writes, the proto option or the
errors an entry point returns, also needs a `CHANGELOG.md` entry.

## Commit style

Use [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/): a short subject
such as `docs: clarify the system scope`, a blank line, then a body that says why.

## Licensing of contributions

No sign-off line and no separately signed agreement are needed. By opening a pull request you
agree that your contribution is released under the project's [LICENSE](LICENSE).
