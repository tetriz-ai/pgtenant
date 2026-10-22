Wires `grpctenant.NewTenantScopeInterceptor` to tenantdb for a notes service whose methods carry each `tenant_scope` option, behind an authentication interceptor that rejects a call without a valid token and sets the verified org with `grpctenant.WithOrgID`. Neither Trust field is on, so a client-sent `org_id` or a request's `organization_id` never picks the tenant. It calls the service over an in-memory connection, and no call leaves the machine.

```sh
go run ./examples/grpc-service
```

```text
ListNotes as tenant A: [A: renew the domain] [A: send the invoice]
ListNotes without a token: Unauthenticated
ListNotes with only a client-sent org_id: Unauthenticated
ListNotes as tenant A, also sending tenant B's org_id: [A: renew the domain] [A: send the invoice]
ListNotes as tenant B: [B: book the venue] [B: order the badges] [B: print the programme]
AddNote without a token, naming tenant A in the request: Unauthenticated
CountAllNotes as tenant A: PermissionDenied
CountAllNotes in system scope: 5
```
