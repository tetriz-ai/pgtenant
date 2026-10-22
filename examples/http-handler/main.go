// Command http-handler serves each request in its tenant's scope with
// tenantdb.RunInScope. It starts a loopback server; no call leaves the machine.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"

	"gorm.io/gorm"

	"github.com/tetriz-ai/pgtenant/internal/demo"
	"github.com/tetriz-ai/pgtenant/tenantdb"
)

// tokens stands in for real authentication: it maps a bearer token to the
// caller's verified tenant.
var tokens = map[string]string{
	"token-a": demo.TenantA,
	"token-b": demo.TenantB,
}

type tenantKey struct{}

// authenticate resolves the caller's tenant, or answers 401.
func authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		org, ok := tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tenantKey{}, org)))
	})
}

// notesDAO reads through tenantdb.DB, so it runs in whatever scope ctx carries.
type notesDAO struct{ db *gorm.DB }

func (d notesDAO) list(ctx context.Context) ([]string, error) {
	var bodies []string
	err := tenantdb.DB(ctx, d.db).Raw("SELECT body FROM notes ORDER BY body").Scan(&bodies).Error
	return bodies, err
}

func (d notesDAO) add(ctx context.Context, org, body string) error {
	return tenantdb.DB(ctx, d.db).Exec("INSERT INTO notes (organization_id, body) VALUES (?, ?)", org, body).Error
}

func handler(dao notesDAO) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		org := r.Context().Value(tenantKey{}).(string)
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			// One short transaction for the write, committed before the reply.
			err := tenantdb.RunInScope(r.Context(), org, tenantdb.ScopeEnforced, func(ctx context.Context) error {
				return dao.add(ctx, org, string(body))
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		notes, err := tenantdb.RunInScopeValue(r.Context(), org, tenantdb.ScopeEnforced, dao.list)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// No transaction may be open across a slow outbound call.
		if tenantdb.HasOpenTx(r.Context()) {
			http.Error(w, "transaction open across an outbound call", http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprintln(w, strings.Join(notes, " | "))
	})
}

func call(url, method, token, body string) {
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		log.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	if token == "" {
		token = "(no token)"
	}
	fmt.Printf("%s %s -> %d %s", method, token, resp.StatusCode, out)
}

func main() {
	db, err := demo.Setup(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	srv := httptest.NewServer(authenticate(handler(notesDAO{db: db})))
	defer srv.Close()

	call(srv.URL, http.MethodGet, "token-a", "")
	call(srv.URL, http.MethodGet, "token-b", "")
	call(srv.URL, http.MethodPost, "token-b", "B: print the programme")
	call(srv.URL, http.MethodGet, "", "")
}
