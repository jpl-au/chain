package chain_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/jpl-au/chain"
)

// FuzzMux_ServeHTTP checks that, for any request, a Mux with passthrough
// middleware and no custom error handlers produces exactly the response a
// plain http.ServeMux with the same routes produces: status, headers and
// body. This covers the capture-and-replay path for every 404, 405 and
// redirect shape the standard library can generate.
func FuzzMux_ServeHTTP(f *testing.F) {
	for _, seed := range [][2]string{
		{http.MethodGet, "/"}, {http.MethodGet, "/x"}, {http.MethodPost, "/x"}, {http.MethodHead, "/x"},
		{http.MethodOptions, "*"}, {http.MethodGet, "/dir"}, {http.MethodGet, "/dir/"}, {http.MethodGet, "//x"},
		{http.MethodGet, "/x/../y"}, {http.MethodGet, "/%2e%2e/x"}, {http.MethodGet, "/users/42"},
		{http.MethodConnect, "/x"}, {http.MethodGet, "/dir/%2F"}, {http.MethodGet, "/x?a=b"},
		{http.MethodGet, "/files/a/b/c"}, {"get", "/x"}, {"", "/x"}, {http.MethodGet, ""}, {http.MethodGet, "x"},
	} {
		f.Add(seed[0], seed[1])
	}

	register := func(handle func(string, func(http.ResponseWriter, *http.Request))) {
		handle("GET /x", echo("x"))
		handle("PUT /x", echo("put"))
		handle("GET /dir/", echo("dir"))
		handle("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(r.PathValue("id"))) })
		handle("GET /files/{p...}", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(r.PathValue("p"))) })
	}
	std := http.NewServeMux()
	register(std.HandleFunc)
	passthrough := func(next http.Handler) http.Handler { return next }
	mux := chain.New().Use(passthrough, passthrough)
	register(func(p string, h func(http.ResponseWriter, *http.Request)) { mux.HandleFunc(p, h) })

	f.Fuzz(func(t *testing.T, method, path string) {
		req, err := http.NewRequest(method, "http://example.com"+path, nil)
		if err != nil {
			t.Skip("not a request net/http accepts")
		}
		want, got := httptest.NewRecorder(), httptest.NewRecorder()
		std.ServeHTTP(want, req.Clone(req.Context()))
		mux.ServeHTTP(got, req.Clone(req.Context()))

		if want.Code != got.Code || want.Body.String() != got.Body.String() || !reflect.DeepEqual(want.Header(), got.Header()) {
			t.Fatalf("%s %q\nstdlib: %d %v %q\nchain:  %d %v %q", method, path,
				want.Code, want.Header(), want.Body.String(), got.Code, got.Header(), got.Body.String())
		}
	})
}
