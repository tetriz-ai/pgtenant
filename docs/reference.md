# Reference

The exact rules for each package. The doc comments on
[pkg.go.dev](https://pkg.go.dev/github.com/tetriz-ai/pgtenant) carry the signatures.

## The policy you need

pgtenant writes `app.current_tenant`. Your policy reads it. This is the one in
`internal/demo/setup.sql`, for a table owned by the application role:

```sql
ALTER TABLE notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE notes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON notes
    USING (current_setting('app.current_tenant', true) = '__system__'
           OR organization_id::text = current_setting('app.current_tenant', true))
    WITH CHECK (current_setting('app.current_tenant', true) = '__system__'
                OR organization_id::text = current_setting('app.current_tenant', true));
```

`SystemTx` and system scopes write `__system__`, which the first branch uses to let those
queries see every row. Comparing as text keeps an unset setting from failing a `uuid` cast.

### Retention windows

`WithDataCutoff` provides an optional retention window: applications can hide rows older than
a calendar date through an RLS policy. It filters reads; it does not delete retained data.

To hide older rows with `WithDataCutoff`, add a second policy marked `AS RESTRICTIVE`, as
`testdata/migrations/0001_notes.sql` does. A plain policy is ORed with `tenant_isolation` and
would show every tenant's rows. System scopes set `app.data_cutoff` to `-infinity`, so they see
every date.

```sql
CREATE POLICY cutoff_window ON notes AS RESTRICTIVE FOR SELECT
    USING (created_on >= coalesce(nullif(current_setting('app.data_cutoff', true), ''), '-infinity')::date);
```

## tenantdb

| Entry point | `app.current_tenant` | `app.data_cutoff` |
|---|---|---|
| `TenantTx`, or a scope with `ScopeEnforced` | the org ID as a canonical UUID | the `WithDataCutoff` date, when set |
| `SystemTx`, or a scope with `ScopeSystem` | `__system__` | `-infinity` |

Every entry point commits when `fn` returns nil. It rolls back on an error, a panic or
`runtime.Goexit`.

`TenantTx` and every `ScopeEnforced` scope refuse a superuser or `BYPASSRLS` role with
`ErrRoleBypassesRLS`, because RLS would not filter that role's rows. A DAO call through
`tenantdb.DB` outside any scope fails with `ErrNoScope`, unless the context explicitly carries
an existing GORM transaction. A carried plain pool is rejected even with the row-lock flag.
An accepted external transaction is caller-managed: `DB` does not set or verify its tenant,
cutoff or role; the caller must do so before querying.

Every error is a `tenantdb.Err*` value for `errors.Is`. The
[package docs](https://pkg.go.dev/github.com/tetriz-ai/pgtenant/tenantdb) say when each occurs.

## grpctenant

The interceptor opens a scope for each RPC from its `tenant_scope` proto option, for the org
your authentication interceptor verified.

| `tenant_scope` | The interceptor |
|---|---|
| `TENANT_SCOPE_ENFORCED` (the enum's zero value) | needs an org, answers `FailedPrecondition` without one and `InvalidArgument` for one that is not a UUID, and runs the handler in that tenant's scope |
| `TENANT_SCOPE_SYSTEM` | runs the handler in a scope for every tenant |
| `TENANT_SCOPE_CALLER_MANAGED` | puts the org in the context and opens nothing. The handler calls `TenantTx` |

`Scopes` must not be nil. An enforced or system RPC also needs `NewContext` and
`CommitOrRollback`. Without one of them the constructor panics at startup and names the missing
field. `NewTenantScopeStreamInterceptor(cfg)` applies the same rules to streaming RPCs.

By default only the org `WithOrgID` set counts. Each flag below lets the caller choose its own
tenant, so enable one only for trusted callers. An org in the context wins over both.

| Flag | Accepts |
|---|---|
| `TrustIncomingMetadata` | an `org_id` in gRPC metadata |
| `TrustRequestOrgID` | the request's `organization_id`, for unary caller-managed RPCs |

## testdb

| Option | What it adds after the dump loads |
|---|---|
| `WithStubTable(ddl...)` | a table the dump references but does not define |
| `WithDefaultPartition(parents...)` | a `DEFAULT` partition, so tests can insert rows outside the dump's partition ranges |
| `WithDropIndex(names...)` | drops an index that parallel tests inserting the same rows would violate |
| `WithPostHook(sql...)` | SQL such as `FORCE ROW LEVEL SECURITY` or a `GRANT` |

`LoadSchema` loads the dump, then applies the options in the order above, in one transaction.

Set `PGTENANT_REQUIRE_TEST_DB=1` in CI. A test then fails instead of skipping when Postgres is
unreachable.

## gormctx

| Call | What `tenantdb.DB` returns for that context |
|---|---|
| `gormctx.New(ctx, tx)` | `tx`, a transaction you already hold, such as the `tx` of `TenantTx`. Inside a scope, `DB` fails with `ErrOuterTransaction` instead |
| `gormctx.NewWithLockKey(ctx)` | A handle whose SELECTs add `FOR UPDATE` |

## Protobuf extension registration

`pgtenant.v1.tenant_scope` uses MethodOptions extension number `2800106`.
This number is not yet reserved in the [global extension registry](https://github.com/protocolbuffers/protobuf/blob/main/docs/options.md).
Another library using the same number on MethodOptions can cause descriptor registration to
fail when both are linked. Check for collisions in applications that combine custom options.
Do not renumber compiled descriptors independently of their source proto files.
