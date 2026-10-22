# Install

pgtenant is a set of Go packages. It needs Go 1.26 or later, as `go.mod` states.

## Add it to your module

1. Fetch the package you need. `tenantdb` is the core. The others are optional.

   ```sh
   go get github.com/tetriz-ai/pgtenant/tenantdb@latest
   ```

2. Open your own database with GORM in place of `demo.Setup`, which is internal to this
   repository. [own-db](../examples/own-db/) shows the call.
3. Supply what the demo created for you:

| You supply | Why |
|---|---|
| Postgres, tested on version 17 | CI and the compose file run Postgres 17 only |
| GORM with `gorm.io/driver/postgres` | pgtenant runs its transactions through GORM |
| A policy that reads `app.current_tenant` | pgtenant writes the setting, and your policy decides which rows match. [The policy you need](reference.md#the-policy-you-need) has one to copy |
| A login role that is not a superuser and has no `BYPASSRLS` | RLS does not filter such a role. [Isolation](limitations.md#isolation) says what pgtenant refuses |
| `FORCE ROW LEVEL SECURITY` on each table your login role owns | Without it the policy skips the table's owner |

## The local Postgres

The tests and examples use the Postgres 17 in `docker-compose.yml`. It listens on
`127.0.0.1:5433` only. The demo creates the table, the policy and an application role on it.

| To | Do this |
|---|---|
| Start it | `docker compose up -d`, or `podman-compose up -d` |
| Stop it | `docker compose down`, or `podman-compose down` |
| Use another port | Change it in `docker-compose.yml`, and set `PGTENANT_TEST_PORT` to match |
| Use another host | Set `PGTENANT_TEST_HOST`. The examples drop and recreate its `pgtenant_example` schema, so use a scratch server |

The examples and tests read both variables. `demo.Setup` resets the demo schema on every run, so
`go run ./examples/read-own-rows` prints the same output each time.
