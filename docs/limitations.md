# Limitations

Each entry is a limit of Postgres or of the design. Where a test pins an entry, the table names
it. Within each group the harmful direction comes first.

## Threat model

RLS protects against application mistakes, such as forgetting a tenant predicate. It is not a
boundary against callers who can execute arbitrary SQL as the application role. PostgreSQL
allows that role to set `app.current_tenant`, including `__system__`; SQL injection or raw SQL
can therefore bypass these policies. Prevent SQL injection and restrict direct SQL access.

A SYSTEM annotation opens a cross-tenant scope; it grants no authorization. Authenticate and
authorize administrators before the tenant interceptor or before accessing a SYSTEM method.

## Isolation

**Can show another tenant's rows:**

| Limit | What to do |
|---|---|
| A table's owner sees every tenant's rows unless the table has `FORCE ROW LEVEL SECURITY`. Pinned by `TestOwnerBypassesRLSWithoutForce` | Force it on each table your login role owns |
| `TrustIncomingMetadata` and `TrustRequestOrgID`, when on, let the caller pick its tenant. Pinned by `grpctenant/tenant_scope_test.go` | Enable one only for trusted callers |
| `SystemTx` is not refused under a superuser or `BYPASSRLS` role. Pinned by `TestRoleThatBypassesRLS` | Run `SystemTx` as your application role |

**Returns no rows and no error (no leak, but easy to miss):**

| Limit | What to do |
|---|---|
| A query through the plain `*gorm.DB`, or a `gormctx` transaction with no scope, sets no tenant. Pinned by `TestNoTenantSetReadsNoRows` | Query through `tenantdb.DB` inside a scope |

## Concurrency

**Can show the wrong rows:** None known.

**Fails the request:**

| Limit | What to do |
|---|---|
| A statement that overlaps another in one scope, as from two goroutines, fails with `ErrScopeBusy`. Pinned by `TestScopeBusy` | Give each goroutine its own scope |
| A transaction held open across a slow call can be ended by `idle_in_transaction_session_timeout`, failing the scope's later queries | Make slow calls before the scope's first query or after it ends. `tenantdb.HasOpenTx(ctx)` reports whether its transaction is open |
| A statement issued while a `*sql.Row` from `Row` is still unscanned fails with `driver: bad connection`, not `ErrScopeBusy` | Scan each `Row` before the scope's next statement |

## Platform

- CI and the compose file run Postgres 17 only. Other versions are untested.
