package main

import (
	"context"
	"testing"

	examplev1 "github.com/tetriz-ai/pgtenant/gen/pgtenant/example/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestSystemMethodRequiresAdmin(t *testing.T) {
	for _, tt := range []struct {
		name, token, method string
		want                codes.Code
	}{
		{"anonymous", "", examplev1.NoteService_CountAllNotes_FullMethodName, codes.Unauthenticated},
		{"tenant", "token-a", examplev1.NoteService_CountAllNotes_FullMethodName, codes.PermissionDenied},
		{"admin", "token-admin", examplev1.NoteService_CountAllNotes_FullMethodName, codes.OK},
		{"tenant own rows", "token-a", examplev1.NoteService_ListNotes_FullMethodName, codes.OK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.token != "" {
				ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", tt.token))
			}
			called := false
			_, err := authenticate(ctx, nil, &grpc.UnaryServerInfo{FullMethod: tt.method}, func(context.Context, any) (any, error) { called = true; return nil, nil })
			if status.Code(err) != tt.want || called != (tt.want == codes.OK) {
				t.Fatalf("code=%v called=%v, want %v", status.Code(err), called, tt.want)
			}
		})
	}
}
