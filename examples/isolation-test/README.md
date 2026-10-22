A test to copy into your own repository: each test gets a fresh database cloned from the committed snapshot in `testdata/schema.sql`, through `testdb.NewMigrator` and pgtestdb. It proves tenant A can neither read nor write tenant B's rows. The command sets `PGTENANT_REQUIRE_TEST_DB=1`, so it fails instead of skipping when no database is reachable.

The test role owns the tables, so the `WithPostHook` line that forces row-level security is what makes the policy apply. Remove it and the test fails.

```sh
PGTENANT_REQUIRE_TEST_DB=1 go test -count=1 ./examples/isolation-test/
```

```text
ok  	github.com/tetriz-ai/pgtenant/examples/isolation-test	<time>
```
