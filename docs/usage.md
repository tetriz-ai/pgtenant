# Usage

Each entry point writes `app.current_tenant` inside the transaction before your first query.
Pick the one that matches what you are building.

| You have | Use | Example |
|---|---|---|
| One block of queries for one tenant | [`tenantdb.TenantTx`](#one-block-of-code-tenanttx) | [read-own-rows](../examples/read-own-rows/), [own-db](../examples/own-db/) |
| DAO methods called from an HTTP handler or a job | [`tenantdb.RunInScope` and `tenantdb.DB`](#dao-code-runinscope-and-db) | [dao-scope](../examples/dao-scope/), [http-handler](../examples/http-handler/) |
| A gRPC service | [`grpctenant`](#grpc-grpctenant) | [grpc-service](../examples/grpc-service/) |
| DAO tests that must run your real policies | [`testdb`](#testing-your-daos-testdb) | [isolation-test](../examples/isolation-test/) |

Each excerpt below comes from the linked example, which CI runs.
[pkg.go.dev](https://pkg.go.dev/github.com/tetriz-ai/pgtenant) has the full API, and
[reference.md](reference.md) has the exact rules.

## One block of code: `TenantTx`

Use `TenantTx` when the queries sit in the one function that holds `tx`, such as a background
job or a handler without a DAO layer. The README's quick start shows a read. A write for another
tenant fails the policy's `WITH CHECK`, as in [isolation-test](../examples/isolation-test/):

```go
		err := tenantdb.TenantTx(ctx, db, tenantA, func(tx *gorm.DB) error {
			return tx.Exec("INSERT INTO notes (organization_id, body) VALUES (?, 'sneaky')", tenantB).Error
		})
```

Query through `tx`. A DAO method that calls `tenantdb.DB(ctx, db)` inside `fn` fails with
`ErrNoScope`, so pass it `gormctx.New(ctx, tx)` or use `RunInScope`.

`SystemTx` is the same for every tenant at once. It writes `__system__`, and your policy
decides what that grants.

## DAO code: `RunInScope` and `DB`

Use these when the caller knows the tenant, but the queries live in DAO methods a few calls
down. Pass `tenantdb.ScopeEnforced` for one tenant or `tenantdb.ScopeSystem` for every tenant.
`RunInScopeValue` is the form for a function that also returns a value. The README's
[Scope DAO methods](../README.md#scope-dao-methods) task shows it with its output.

The first `DB` call in a scope opens the transaction, and later calls reuse it. An inner
`RunInScope` runs in its own transaction for its own tenant, and the outer scope resumes after
it.

`tenantdb.WithDataCutoff` sets a date that a second, `AS RESTRICTIVE` policy compares against to
hide older rows. [reference.md](reference.md#the-policy-you-need) shows that policy.

## gRPC: `grpctenant`

Use `grpctenant` to declare each RPC's tenant scope once in the proto, rather than open it in
every handler.

1. Copy `proto/pgtenant/v1/tenant_scope.proto` into your proto root, keep its `go_package`, and
   import it.
2. Leave it out of your generation with `exclude_paths: [proto/pgtenant]` on your `buf.gen.yaml`
   input, so your code imports pgtenant's `gen/pgtenant/v1`.
3. Annotate every tenant RPC, as [notes.proto](../proto/pgtenant/example/v1/notes.proto) does,
   since an RPC without the option gets no scope.

   ```proto
     rpc ListNotes(ListNotesRequest) returns (ListNotesResponse) {
       option (pgtenant.v1.tenant_scope) = TENANT_SCOPE_ENFORCED;
     }
   ```

4. Pass `grpctenant.NewTenantScopeInterceptor` a config built from tenantdb's functions, as
   [grpc-service](../examples/grpc-service/) does:

   ```go
   	return grpctenant.TenantScopeConfig{
   		Scopes: grpctenant.LoadTenantScopesFromRegistry(),
   		NewContext: func(ctx context.Context, orgID string, scope int) context.Context {
   			return tenantdb.NewContext(ctx, orgID, tenantdb.Scope(scope))
   		},
   		CommitOrRollback: tenantdb.CommitOrRollback,
   	}
   ```

5. Chain it after the interceptor that authenticates the caller and calls
   `grpctenant.WithOrgID`, as in
   `grpc.ChainUnaryInterceptor(authenticate, grpctenant.NewTenantScopeInterceptor(cfg))`.

## Testing your DAOs: `testdb`

Use `testdb` to run DAO tests against your real schema and policies, so a test catches a
migration that drops or loosens a policy.

1. Commit a schema-only dump of the database your migrations build, written by the `pg_dump` of
   your own Postgres installation:

   ```sh
   pg_dump --schema-only --no-owner --no-privileges my_app_db > testdata/schema.sql
   ```

2. Build a migrator from that dump. Each test then gets a fresh database cloned from it. From
   [isolation-test](../examples/isolation-test/):

   ```go
   var migrator = testdb.NewMigrator(os.DirFS("../../testdata"), "schema.sql",
   	testdb.WithPostHook("ALTER TABLE notes FORCE ROW LEVEL SECURITY"))
   ```

   The test role owns the tables, so force row-level security on every table with a policy, not
   only `notes`. Add one `ALTER TABLE ... FORCE ROW LEVEL SECURITY` per table to `WithPostHook`.

3. Give your test Postgres role the right to create roles and databases.
   [pgtestdb](https://github.com/peterldowns/pgtestdb) clones a database for each test.
