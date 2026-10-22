Serves each HTTP request in its caller's tenant scope with `tenantdb.RunInScope` and `tenantdb.RunInScopeValue`, behind a stand-in authentication middleware. The DAO reads through `tenantdb.DB`, so it needs no tenant argument. The example starts a loopback server, and no call leaves the machine.

```sh
go run ./examples/http-handler
```

```text
GET token-a -> 200 A: renew the domain | A: send the invoice
GET token-b -> 200 B: book the venue | B: order the badges
POST token-b -> 200 B: book the venue | B: order the badges | B: print the programme
GET (no token) -> 401 unauthenticated
```
