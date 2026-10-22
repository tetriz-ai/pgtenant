Runs one DAO method, which queries through `tenantdb.DB` and takes no tenant argument, inside tenant B's scope with `tenantdb.RunInScopeValue`, then outside any scope. Inside the scope it counts tenant B's two notes. Outside it fails with `tenantdb.ErrNoScope` instead of returning an empty result.

```sh
go run ./examples/dao-scope
```

```text
in tenant B's scope: 2 <nil>
outside any scope: tenantdb: no scope in context
```
