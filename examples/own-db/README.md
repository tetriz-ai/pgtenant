Reads tenant A's notes using only the public API: it opens the database in `DATABASE_URL` with `gorm.Open(postgres.Open(...))` in place of the repository-internal `demo.Setup`, then runs the same `TenantTx` call as the quick start. It needs the role and schema that the other examples create, so run it after `go run ./examples/read-own-rows`, as the application role.

```sh
DATABASE_URL="postgres://pgtenant_app:app@${PGTENANT_TEST_HOST:-127.0.0.1}:${PGTENANT_TEST_PORT:-5433}/postgres?sslmode=disable&search_path=pgtenant_example" go run ./examples/own-db
```

```text
A: renew the domain | A: send the invoice
```
