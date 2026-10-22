Reads notes inside tenant A's scope with `tenantdb.TenantTx`, from a table that holds two tenants' notes behind a row-level security policy, and only tenant A's come back. This is the README's quick start. `demo.Setup` resets the example schema from `internal/demo/setup.sql` and connects as the application role.

```sh
go run ./examples/read-own-rows
```

```text
A: renew the domain | A: send the invoice
```
