# Examples

| You want | Example |
|---|---|
| To read one tenant's rows | [read-own-rows](read-own-rows/) |
| To open your own database instead of the demo helper | [own-db](own-db/) |
| A DAO method that fails outside a tenant's scope | [dao-scope](dao-scope/) |
| A test that proves tenants cannot see each other | [isolation-test](isolation-test/) |
| Tenant scopes for an HTTP service | [http-handler](http-handler/) |
| Tenant scopes for a gRPC service | [grpc-service](grpc-service/) |

Run them all with `scripts/run-examples.sh` from the repo root. They need the compose Postgres (`docker compose up -d`), and no call leaves the machine. The runner rewrites the duration at the end of a `go test` line to `<time>`.

Each example starts from `demo.Setup`, which resets the demo's notes, except own-db, which reads what the others left. The http-handler and grpc-service examples each add a note for tenant B.

The examples are under the repository's MIT License ([LICENSE](../LICENSE)), so a copy carries those terms.
