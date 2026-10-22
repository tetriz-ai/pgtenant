# CLAUDE.md

## What this is

pgtenant runs GORM queries in Postgres transactions that first set `app.current_tenant`, so row-level security policies filter rows by tenant: `tenantdb` for the transactions, `grpctenant` for a gRPC interceptor driven by a proto option, `testdb` for loading a `pg_dump` schema into pgtestdb test databases.
It writes no policies and does no authentication. Both are the user's.

## Layout

- `tenantdb/`: scopes and transactions. `tenantdb.TenantTx(ctx, db, orgID, fn)` is the smallest entry point. `scope_test.go`, `role_test.go` and `busy_test.go` hold the tests against Postgres, and `unit_test.go` needs none.
- `grpctenant/`: the unary and stream interceptors, and `OrgIDFromContext`/`WithOrgID`.
- `gormctx/`: a `*gorm.DB` and a row-lock flag carried in a context.
- `testdb/`: `NewMigrator`, `LoadSchema` and its options, `Ping`, `SkipOrFail`.
- `proto/pgtenant/v1/tenant_scope.proto`: the option. `proto/pgtenant/example/v1/notes.proto`: the service the tests and the gRPC example use. `gen/`: code `buf generate` writes from both.
- `testdata/migrations/0001_notes.sql`: the sample schema. `testdata/schema.sql`: its `pg_dump --schema-only --no-owner --no-privileges` output.
- `internal/demo/`: `setup.sql` and `demo.Setup(ctx)`, which resets the `pgtenant_example` schema and connects as a role the policy applies to. `demo.TenantA` and `demo.TenantB` have 2 notes each. `internal/testpg/`: the tests' connection settings.
- `examples/`: one folder per example, indexed in `examples/README.md`. Copy `examples/read-own-rows` for a new one. `readme_test.go` keeps each code excerpt the README shows a verbatim part of its example, and the quick start example the same as `ExampleTenantTx`. `examples_runner_test.go` pins that the runner stops when the index and the folders disagree.
- `docker-compose.yml`: Postgres 17 on 127.0.0.1:5433.

## Commands

Go 1.26 is the floor. CI tests 1.26 and 1.27 with a Postgres 17 service on port 5433. These are CI's steps:

```sh
docker compose up -d   # or: podman-compose up -d
go build ./...
go vet ./...
PGTENANT_REQUIRE_TEST_DB=1 go test -race -count=1 ./...
GOTOOLCHAIN=go1.26.0 golangci-lint run
scripts/run-examples.sh
```

If a Postgres already listens on 5433, reuse it, or set `PGTENANT_TEST_HOST` and `PGTENANT_TEST_PORT`: the tests and the examples both read them. Lint uses golangci-lint v2.11.4, built and run with Go 1.26 as the Lint job does: on Go 1.27 it panics. The install lines come from CONTRIBUTING.md:

```sh
GOTOOLCHAIN=go1.26.0 go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1
go install github.com/bufbuild/buf/cmd/buf@v1.67.0
```

After editing a `.proto` file, run `buf lint`, then `buf generate`. CI's Generated code job runs both and fails when `gen/` changes. CI runs govulncheck on Go 1.27, on pushes to main and weekly, not on pull requests. Locally:

```sh
GOTOOLCHAIN=go1.27.3 go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
```

## Contract to keep

- A tenant never sees another tenant's rows under `FORCE`. Pinned by `TestTenantIsolation` in `tenantdb/scope_test.go`.
- An org ID must be a UUID and is written in canonical form, checked before any round trip. Pinned by `TestCanonicalOrgID` and `TestValidationBeforeBegin` in `tenantdb/unit_test.go`.
- Commit on nil, rollback on an error or a panic, `fn`'s error unwrapped. Pinned by `TestTransactionOutcomes`.
- `DB` never runs a query it cannot run safely: it returns a handle with `Error` set. Pinned by `TestDBGuards` and `TestScopeValues`.
- An enforced scope or `TenantTx` under a superuser or `BYPASSRLS` role fails with `ErrRoleBypassesRLS`, checked on the role in effect for each transaction. Pinned by `TestRoleThatBypassesRLS`.
- A statement that overlaps another in one scope fails with `ErrScopeBusy` and never reaches the connection. Pinned by `TestScopeBusy`.
- The interceptor constructors panic, naming the field, on a nil `Scopes` or a config an annotated method cannot run with. Pinned by `TestInterceptorConfigChecked`.
- A client-sent `org_id` or a request's `organization_id` counts only with `TrustIncomingMetadata` or `TrustRequestOrgID`, and a verified context org wins. Pinned by `grpctenant/tenant_scope_test.go`.

## Making a change

- Write the test first. A fix needs a case that fails before it and passes after. Tests are table-driven.
- A database test calls `newGormDB(t, ...)` in `tenantdb/main_test.go`, which skips without Postgres unless `PGTENANT_REQUIRE_TEST_DB=1`, `PGTENANT_TEST_HOST` or `PGTENANT_TEST_PORT` is set. `TestCheck` in `internal/testpg/testpg_test.go` pins that.
- A change a user can see gets a `CHANGELOG.md` line under Unreleased. Tests, docs, CI and examples need none.
- Do not silence a lint finding with an inline `//nolint`. Fix the code.
- The examples runner runs each example README's `sh` block and compares standard output with its last `text` block. Run it and `go test` one after the other: both use the `pgtenant_example` schema.
- Files under `examples/` carry no license header. Every other `.go`, `.proto`, `.sh` and `.sql` file does, except the generated `testdata/schema.sql`.
- Commits use Conventional Commits.

## Do not

- Do not edit `gen/` or `testdata/schema.sql` by hand. Regenerate them with `buf generate`, or with the `pg_dump` command above against a database built from `testdata/migrations/`.
- Do not raise `go 1.26` in `go.mod`. It is the floor `golang.org/x/net` sets.
- Do not move the lint job to Go 1.27. golangci-lint v2.11 cannot read Go 1.27 export data.
- Do not point the examples at a database you care about. `demo.Setup` drops and recreates `pgtenant_example`.
