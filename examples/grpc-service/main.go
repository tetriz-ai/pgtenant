// Command grpc-service wires the grpctenant interceptor to tenantdb for a small
// notes service, and calls it over an in-memory connection. No call leaves the
// machine.
package main

import (
	"context"
	"fmt"
	"log"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"gorm.io/gorm"

	examplev1 "github.com/tetriz-ai/pgtenant/gen/pgtenant/example/v1"
	"github.com/tetriz-ai/pgtenant/grpctenant"
	"github.com/tetriz-ai/pgtenant/internal/demo"
	"github.com/tetriz-ai/pgtenant/tenantdb"
)

// tenantConfig connects the interceptor to tenantdb's scopes.
func tenantConfig() grpctenant.TenantScopeConfig {
	return grpctenant.TenantScopeConfig{
		Scopes: grpctenant.LoadTenantScopesFromRegistry(),
		NewContext: func(ctx context.Context, orgID string, scope int) context.Context {
			return tenantdb.NewContext(ctx, orgID, tenantdb.Scope(scope))
		},
		CommitOrRollback: tenantdb.CommitOrRollback,
	}
}

// tokens stands in for real authentication.
var tokens = map[string]struct {
	org   string
	admin bool
}{
	"token-a":     {org: demo.TenantA},
	"token-b":     {org: demo.TenantB},
	"token-admin": {org: demo.TenantA, admin: true},
}

// authenticate runs before the tenant interceptor. It rejects a call without a
// valid token and puts the verified org in context with WithOrgID. The tenant
// interceptor ignores a client-sent org_id, because neither Trust field is on.
func authenticate(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	tok := md.Get("authorization")
	if len(tok) != 1 {
		return nil, status.Error(codes.Unauthenticated, "missing token")
	}
	caller, ok := tokens[tok[0]]
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "unknown token")
	}
	if info.FullMethod == examplev1.NoteService_CountAllNotes_FullMethodName && !caller.admin {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
	}
	return next(grpctenant.WithOrgID(ctx, caller.org), req)
}

type notes struct {
	examplev1.UnimplementedNoteServiceServer
	db *gorm.DB
}

// ListNotes is TENANT_SCOPE_ENFORCED: the interceptor opened the scope.
func (s notes) ListNotes(ctx context.Context, _ *examplev1.ListNotesRequest) (*examplev1.ListNotesResponse, error) {
	var rows []struct{ Body string }
	if err := tenantdb.DB(ctx, s.db).Raw("SELECT body FROM notes ORDER BY body").Scan(&rows).Error; err != nil {
		return nil, err
	}
	resp := &examplev1.ListNotesResponse{}
	for _, r := range rows {
		resp.Notes = append(resp.Notes, &examplev1.Note{Body: r.Body})
	}
	return resp, nil
}

// CountAllNotes is TENANT_SCOPE_SYSTEM: the scope sees every tenant.
func (s notes) CountAllNotes(ctx context.Context, _ *examplev1.CountAllNotesRequest) (*examplev1.CountAllNotesResponse, error) {
	var n int64
	err := tenantdb.DB(ctx, s.db).Raw("SELECT count(*) FROM notes").Scan(&n).Error
	return &examplev1.CountAllNotesResponse{Count: n}, err
}

// AddNote is TENANT_SCOPE_CALLER_MANAGED: the handler opens its own transaction.
func (s notes) AddNote(ctx context.Context, req *examplev1.AddNoteRequest) (*examplev1.AddNoteResponse, error) {
	org := grpctenant.OrgIDFromContext(ctx)
	err := tenantdb.TenantTx(ctx, s.db, org, func(tx *gorm.DB) error {
		return tx.Exec("INSERT INTO notes (organization_id, body) VALUES (?, ?)", org, req.GetBody()).Error
	})
	return &examplev1.AddNoteResponse{Note: &examplev1.Note{OrganizationId: org, Body: req.GetBody()}}, err
}

func main() {
	db, err := demo.Setup(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(authenticate, grpctenant.NewTenantScopeInterceptor(tenantConfig())))
	examplev1.RegisterNoteServiceServer(srv, notes{db: db})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client := examplev1.NewNoteServiceClient(conn)
	as := func(pairs ...string) context.Context {
		return metadata.NewOutgoingContext(context.Background(), metadata.Pairs(pairs...))
	}

	list := func(label string, ctx context.Context) {
		resp, err := client.ListNotes(ctx, &examplev1.ListNotesRequest{})
		if err != nil {
			fmt.Printf("ListNotes %s: %s\n", label, status.Code(err))
			return
		}
		fmt.Printf("ListNotes %s:", label)
		for _, n := range resp.GetNotes() {
			fmt.Printf(" [%s]", n.GetBody())
		}
		fmt.Println()
	}
	list("as tenant A", as("authorization", "token-a"))
	list("without a token", context.Background())
	list("with only a client-sent org_id", as("org_id", demo.TenantB))
	list("as tenant A, also sending tenant B's org_id", as("authorization", "token-a", "org_id", demo.TenantB))

	if _, err := client.AddNote(as("authorization", "token-b"), &examplev1.AddNoteRequest{Body: "B: print the programme"}); err != nil {
		log.Fatal(err)
	}
	list("as tenant B", as("authorization", "token-b"))

	_, err = client.AddNote(context.Background(), &examplev1.AddNoteRequest{OrganizationId: demo.TenantA, Body: "planted"})
	fmt.Println("AddNote without a token, naming tenant A in the request:", status.Code(err))

	_, err = client.CountAllNotes(as("authorization", "token-a"), &examplev1.CountAllNotesRequest{})
	fmt.Println("CountAllNotes as tenant A:", status.Code(err))
	count, err := client.CountAllNotes(as("authorization", "token-admin"), &examplev1.CountAllNotesRequest{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("CountAllNotes in system scope:", count.GetCount())
}
