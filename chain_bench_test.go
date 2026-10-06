package chain_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jpl-au/chain"
)

// BenchmarkMux_ServeHTTP compares chain against a plain http.ServeMux on the
// routed path, with and without middleware, and on the unrouted path where a
// ServeMux-generated 404 is captured and replayed.
func BenchmarkMux_ServeHTTP(b *testing.B) {
	passthrough := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
	}
	notFound := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("custom"))
	})

	std := http.NewServeMux()
	std.HandleFunc("GET /users/{id}", echo("ok"))

	cases := []struct {
		name string
		h    http.Handler
		path string
	}{
		{"stdlib/routed", std, "/users/42"},
		{"stdlib/404", std, "/missing"},
		{"chain/routed/no-middleware", chain.New().HandleFunc("GET /users/{id}", echo("ok")), "/users/42"},
		{"chain/routed/three-middleware", chain.New().Use(passthrough, passthrough, passthrough).HandleFunc("GET /users/{id}", echo("ok")), "/users/42"},
		{"chain/404/replayed", chain.New().Use(passthrough).HandleFunc("GET /users/{id}", echo("ok")), "/missing"},
		{"chain/404/custom-handler", chain.New().Use(passthrough).WithNotFound(notFound).HandleFunc("GET /users/{id}", echo("ok")), "/missing"},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			req := httptest.NewRequest(http.MethodGet, c.path, nil)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				c.h.ServeHTTP(httptest.NewRecorder(), req)
			}
		})
	}
}
