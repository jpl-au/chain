package chain_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jpl-au/chain"
)

// Tests are organised by the method under test (TestMux_Method) with one
// subtest per behaviour. Shared helpers live at the top of this file.

// response is an HTTP response reduced to what the tests assert on.
type response struct {
	status int
	header http.Header
	body   string
}

// serve starts a test server for h. Panics inside handlers are expected by some
// tests, so the server's error log is silenced to keep test output readable.
func serve(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// testClient does not follow redirects and does not reuse connections, so a
// connection the server aborted (for example after a panic) is never retried.
var testClient = &http.Client{
	Transport: &http.Transport{DisableKeepAlives: true},
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// do sends one request to srv without following redirects.
func do(t *testing.T, srv *httptest.Server, method, path string) response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest(%s %s): %v", method, path, err)
	}
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: reading body: %v", method, path, err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: string(body)}
}

// request serves h and sends a single request to it.
func request(t *testing.T, h http.Handler, method, path string) response {
	t.Helper()
	return do(t, serve(t, h), method, path)
}

// echo returns a handler that writes body.
func echo(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }
}

// setHeader is middleware that sets a response header before calling next.
func setHeader(name, value string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(name, value)
			next.ServeHTTP(w, r)
		})
	}
}

// trace is middleware that records name+":before" and name+":after" around next.
func trace(order *[]string, name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*order = append(*order, name+":before")
			next.ServeHTTP(w, r)
			*order = append(*order, name+":after")
		})
	}
}

// observed is what inspect middleware records after the handler has run.
type observed struct {
	method, path string
	status, size int
	written      bool
}

// inspect is middleware that records the request and the wrapper's state after next returns.
func inspect(log *[]observed) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			rw, ok := chain.Writer(w)
			if !ok {
				panic("chain.Writer did not find the response writer")
			}
			*log = append(*log, observed{r.Method, r.URL.Path, rw.Status(), rw.Size(), rw.Written()})
		})
	}
}

// expectPanic runs fn and fails unless it panics with exactly want.
func expectPanic(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic %q, got none", want)
		}
		if got := fmt.Sprint(r); got != want {
			t.Errorf("panic = %q, want %q", got, want)
		}
	}()
	fn()
}

// unwrapping is a writer wrapper that follows the Unwrap convention.
type unwrapping struct{ http.ResponseWriter }

func (u unwrapping) Unwrap() http.ResponseWriter { return u.ResponseWriter }

// opaque is a writer wrapper that hides what it wraps.
type opaque struct{ http.ResponseWriter }

func TestWriter(t *testing.T) {
	t.Run("finds the writer", func(t *testing.T) {
		tests := []struct {
			name string
			wrap func(http.ResponseWriter) http.ResponseWriter
			want bool
		}{
			{"direct", func(w http.ResponseWriter) http.ResponseWriter { return w }, true},
			{"behind one Unwrap wrapper", func(w http.ResponseWriter) http.ResponseWriter { return unwrapping{w} }, true},
			{"behind two Unwrap wrappers", func(w http.ResponseWriter) http.ResponseWriter { return unwrapping{unwrapping{w}} }, true},
			{"behind a wrapper without Unwrap", func(w http.ResponseWriter) http.ResponseWriter { return opaque{w} }, false},
			{"Unwrap wrapper over an opaque one", func(w http.ResponseWriter) http.ResponseWriter { return unwrapping{opaque{w}} }, false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var got chain.ResponseWriter
				var ok bool
				mux := chain.New().
					Use(func(next http.Handler) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							next.ServeHTTP(tt.wrap(w), r)
						})
					}).
					Use(func(next http.Handler) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							next.ServeHTTP(w, r)
							got, ok = chain.Writer(w)
						})
					}).
					HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusCreated)
					})
				request(t, mux, http.MethodGet, "/")

				if ok != tt.want {
					t.Fatalf("Writer() ok = %v, want %v", ok, tt.want)
				}
				if ok && got.Status() != http.StatusCreated {
					t.Errorf("found writer reports Status() = %d, want 201", got.Status())
				}
			})
		}
	})

	t.Run("reports false outside a mux", func(t *testing.T) {
		if _, ok := chain.Writer(httptest.NewRecorder()); ok {
			t.Error("Writer() found a chain writer on a plain recorder")
		}
	})

	t.Run("reports false for nil", func(t *testing.T) {
		if _, ok := chain.Writer(nil); ok {
			t.Error("Writer(nil) reported ok")
		}
	})
}

func TestNew(t *testing.T) {
	t.Run("returns a mux that serves requests", func(t *testing.T) {
		mux := chain.New()
		if mux == nil {
			t.Fatal("New() returned nil")
		}
		if got := request(t, mux, http.MethodGet, "/"); got.status != http.StatusNotFound {
			t.Errorf("empty mux status = %d, want 404", got.status)
		}
	})
}

func TestMux_Use(t *testing.T) {
	t.Run("applies middleware to routes", func(t *testing.T) {
		tests := []struct {
			name     string
			register func(*chain.Mux)
			want     []string
		}{
			{
				name:     "single call",
				register: func(m *chain.Mux) { m.Use(setHeader("X-1", "on")) },
				want:     []string{"X-1"},
			},
			{
				name: "separate calls",
				register: func(m *chain.Mux) {
					m.Use(setHeader("X-1", "on"))
					m.Use(setHeader("X-2", "on"))
				},
				want: []string{"X-1", "X-2"},
			},
			{
				name:     "variadic call",
				register: func(m *chain.Mux) { m.Use(setHeader("X-1", "on"), setHeader("X-2", "on")) },
				want:     []string{"X-1", "X-2"},
			},
			{
				name: "chained calls",
				register: func(m *chain.Mux) {
					m.Use(setHeader("X-1", "on")).Use(setHeader("X-2", "on"))
				},
				want: []string{"X-1", "X-2"},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				mux := chain.New()
				tt.register(mux)
				mux.HandleFunc("GET /", echo("ok"))

				got := request(t, mux, http.MethodGet, "/")
				for _, h := range tt.want {
					if got.header.Get(h) != "on" {
						t.Errorf("header %s not set: middleware did not run", h)
					}
				}
			})
		}
	})

	t.Run("runs middleware in registration order", func(t *testing.T) {
		var order []string
		mux := chain.New().
			Use(trace(&order, "first")).
			Use(trace(&order, "second")).
			HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "handler")
			})

		request(t, mux, http.MethodGet, "/")

		want := []string{"first:before", "second:before", "handler", "second:after", "first:after"}
		if !reflect.DeepEqual(order, want) {
			t.Errorf("order = %v, want %v", order, want)
		}
	})

	t.Run("only affects routes registered afterwards", func(t *testing.T) {
		mux := chain.New()
		mux.HandleFunc("GET /before", echo("ok"))
		mux.Use(setHeader("X-Late", "on"))
		mux.HandleFunc("GET /after", echo("ok"))

		srv := serve(t, mux)
		if do(t, srv, http.MethodGet, "/before").header.Get("X-Late") != "" {
			t.Error("middleware registered after a route was applied to it")
		}
		if do(t, srv, http.MethodGet, "/after").header.Get("X-Late") != "on" {
			t.Error("middleware was not applied to a route registered after it")
		}
	})

	t.Run("panics on nil middleware", func(t *testing.T) {
		expectPanic(t, "chain: nil middleware passed to Use", func() {
			chain.New().Use(nil)
		})
	})
}

func TestMux_Handle(t *testing.T) {
	t.Run("registers a handler", func(t *testing.T) {
		mux := chain.New().Handle("GET /hello", http.HandlerFunc(echo("Hello, World!")))

		got := request(t, mux, http.MethodGet, "/hello")
		if got.status != http.StatusOK || got.body != "Hello, World!" {
			t.Errorf("got %d %q, want 200 \"Hello, World!\"", got.status, got.body)
		}
	})

	t.Run("mounts a nested mux", func(t *testing.T) {
		var outer, inner []observed
		child := chain.New().
			Use(inspect(&inner)).
			WithNotFound(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte("inner 404"))
			})).
			HandleFunc("GET /api/ping", echo("pong"))
		parent := chain.New().Use(inspect(&outer)).Handle("/api/", child)

		srv := serve(t, parent)
		if got := do(t, srv, http.MethodGet, "/api/ping"); got.body != "pong" {
			t.Errorf("routed body = %q, want pong", got.body)
		}
		if got := do(t, srv, http.MethodGet, "/api/missing"); got.status != 404 || got.body != "inner 404" {
			t.Errorf("unmatched: got %d %q, want 404 \"inner 404\"", got.status, got.body)
		}

		// Both muxes' middleware see the inner response's status and size.
		if len(inner) != 2 || inner[1].status != 404 || inner[1].size != len("inner 404") {
			t.Errorf("inner middleware log = %+v, want second entry 404 with size %d", inner, len("inner 404"))
		}
		if len(outer) != 2 || outer[1].status != 404 || outer[1].size != len("inner 404") {
			t.Errorf("outer middleware log = %+v, want second entry 404 with size %d", outer, len("inner 404"))
		}
	})

	t.Run("panics on nil handler", func(t *testing.T) {
		expectPanic(t, "chain: nil handler passed to Handle", func() {
			chain.New().Handle("/", nil)
		})
	})
}

func TestMux_HandleFunc(t *testing.T) {
	t.Run("routes using standard library patterns", func(t *testing.T) {
		mux := chain.New().
			HandleFunc("GET /hello", echo("Hello, World!")).
			HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("user: " + r.PathValue("id")))
			}).
			HandleFunc("/any", echo("any method"))

		tests := []struct {
			method, path string
			wantStatus   int
			wantBody     string
		}{
			{http.MethodGet, "/hello", 200, "Hello, World!"},
			{http.MethodGet, "/users/123", 200, "user: 123"},
			{http.MethodPost, "/any", 200, "any method"},
			{http.MethodPost, "/hello", 405, "Method Not Allowed\n"},
			{http.MethodGet, "/missing", 404, "404 page not found\n"},
		}
		srv := serve(t, mux)
		for _, tt := range tests {
			t.Run(tt.method+" "+tt.path, func(t *testing.T) {
				got := do(t, srv, tt.method, tt.path)
				if got.status != tt.wantStatus || got.body != tt.wantBody {
					t.Errorf("got %d %q, want %d %q", got.status, got.body, tt.wantStatus, tt.wantBody)
				}
			})
		}
	})

	t.Run("preserves headers set by the handler", func(t *testing.T) {
		mux := chain.New().HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Custom", "value")
			w.Write([]byte("ok"))
		})
		if got := request(t, mux, http.MethodGet, "/"); got.header.Get("X-Custom") != "value" {
			t.Errorf("X-Custom = %q, want value", got.header.Get("X-Custom"))
		}
	})

	t.Run("panics on nil handler", func(t *testing.T) {
		expectPanic(t, "chain: nil handler passed to HandleFunc", func() {
			chain.New().HandleFunc("/", nil)
		})
	})
}

func TestMux_Group(t *testing.T) {
	t.Run("scopes middleware to the group's routes", func(t *testing.T) {
		mux := chain.New().Use(setHeader("X-Global", "on"))
		mux.HandleFunc("GET /global", echo("global"))
		mux.Group(func(g *chain.Mux) {
			g.Use(setHeader("X-Group", "on"))
			g.HandleFunc("GET /group", echo("group"))
			g.Group(func(n *chain.Mux) {
				n.Use(setHeader("X-Nested", "on"))
				n.HandleFunc("GET /nested", echo("nested"))
			})
		})
		mux.HandleFunc("GET /after", echo("after"))

		tests := []struct {
			path string
			want map[string]bool // header -> expected present
		}{
			{"/global", map[string]bool{"X-Global": true, "X-Group": false, "X-Nested": false}},
			{"/group", map[string]bool{"X-Global": true, "X-Group": true, "X-Nested": false}},
			{"/nested", map[string]bool{"X-Global": true, "X-Group": true, "X-Nested": true}},
			{"/after", map[string]bool{"X-Global": true, "X-Group": false, "X-Nested": false}},
		}
		srv := serve(t, mux)
		for _, tt := range tests {
			t.Run(tt.path, func(t *testing.T) {
				got := do(t, srv, http.MethodGet, tt.path)
				for h, want := range tt.want {
					if present := got.header.Get(h) == "on"; present != want {
						t.Errorf("header %s present = %v, want %v", h, present, want)
					}
				}
			})
		}
	})

	t.Run("group middleware sees the response", func(t *testing.T) {
		var log []observed
		mux := chain.New()
		mux.Group(func(g *chain.Mux) {
			g.Use(inspect(&log))
			g.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				w.Write([]byte("group content"))
			})
		})

		request(t, mux, http.MethodGet, "/")
		want := observed{http.MethodGet, "/", http.StatusAccepted, len("group content"), true}
		if len(log) != 1 || log[0] != want {
			t.Errorf("observed %+v, want [%+v]", log, want)
		}
	})

	t.Run("inherits the route prefix", func(t *testing.T) {
		mux := chain.New().Route("/api", func(api *chain.Mux) {
			api.Group(func(g *chain.Mux) {
				g.Use(setHeader("X-Authed", "on"))
				g.HandleFunc("GET /secret", echo("secret"))
			})
		})

		got := request(t, mux, http.MethodGet, "/api/secret")
		if got.body != "secret" || got.header.Get("X-Authed") != "on" {
			t.Errorf("got %d %q with X-Authed=%q, want secret with middleware applied", got.status, got.body, got.header.Get("X-Authed"))
		}
	})

	t.Run("returns the parent for chaining", func(t *testing.T) {
		mux := chain.New()
		if got := mux.Group(func(*chain.Mux) {}); got != mux {
			t.Error("Group did not return the parent mux")
		}
	})

	t.Run("panics on nil function", func(t *testing.T) {
		expectPanic(t, "chain: nil function passed to Group", func() {
			chain.New().Group(nil)
		})
	})
}

func TestMux_Route(t *testing.T) {
	t.Run("prefixes patterns", func(t *testing.T) {
		tests := []struct {
			name    string
			prefix  string
			pattern string
			path    string
		}{
			{"method and path", "/api", "GET /users", "/api/users"},
			{"path only", "/api", "/health", "/api/health"},
			{"wildcard", "/api", "GET /users/{id}", "/api/users/42"},
			{"exact match marker", "/api", "GET /{$}", "/api/"},
			{"trailing slash on prefix", "/api/", "GET /users", "/api/users"},
			{"host in pattern", "/api", "GET 127.0.0.1/users", "/api/users"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				mux := chain.New().Route(tt.prefix, func(r *chain.Mux) {
					r.HandleFunc(tt.pattern, echo("matched"))
				})
				if got := request(t, mux, http.MethodGet, tt.path); got.body != "matched" {
					t.Errorf("%s: got %d %q, want matched", tt.path, got.status, got.body)
				}
			})
		}
	})

	t.Run("exposes path values", func(t *testing.T) {
		mux := chain.New().Route("/api", func(api *chain.Mux) {
			api.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("user: " + r.PathValue("id")))
			})
		})
		if got := request(t, mux, http.MethodGet, "/api/users/123"); got.body != "user: 123" {
			t.Errorf("body = %q, want \"user: 123\"", got.body)
		}
	})

	t.Run("nests prefixes", func(t *testing.T) {
		mux := chain.New().Route("/api", func(api *chain.Mux) {
			api.Route("/v1", func(v1 *chain.Mux) { v1.HandleFunc("GET /users", echo("v1")) })
			api.Route("/v2", func(v2 *chain.Mux) { v2.HandleFunc("GET /users", echo("v2")) })
		})

		srv := serve(t, mux)
		for _, tt := range []struct{ path, want string }{{"/api/v1/users", "v1"}, {"/api/v2/users", "v2"}} {
			if got := do(t, srv, http.MethodGet, tt.path); got.body != tt.want {
				t.Errorf("%s body = %q, want %q", tt.path, got.body, tt.want)
			}
		}
	})

	t.Run("combines route and global middleware", func(t *testing.T) {
		mux := chain.New().Use(setHeader("X-Global", "on"))
		mux.Route("/api", func(api *chain.Mux) {
			api.Use(setHeader("X-Route", "on"))
			api.HandleFunc("GET /test", echo("ok"))
		})
		mux.HandleFunc("GET /root", echo("root"))

		srv := serve(t, mux)
		got := do(t, srv, http.MethodGet, "/api/test")
		if got.header.Get("X-Global") != "on" || got.header.Get("X-Route") != "on" {
			t.Errorf("headers = %v, want both X-Global and X-Route", got.header)
		}
		if got := do(t, srv, http.MethodGet, "/root"); got.header.Get("X-Route") != "" {
			t.Error("route middleware leaked to a route outside the Route")
		}
	})

	t.Run("returns the parent for chaining", func(t *testing.T) {
		mux := chain.New()
		if got := mux.Route("/api", func(*chain.Mux) {}); got != mux {
			t.Error("Route did not return the parent mux")
		}
	})

	t.Run("panics on invalid input", func(t *testing.T) {
		tests := []struct {
			name string
			fn   func()
			want string
		}{
			{
				name: "nil function",
				fn:   func() { chain.New().Route("/api", nil) },
				want: "chain: nil function passed to Route",
			},
			{
				name: "prefix without leading slash",
				fn:   func() { chain.New().Route("api", func(*chain.Mux) {}) },
				want: "chain: route prefix api must begin with /",
			},
			{
				name: "pattern without a path",
				fn: func() {
					chain.New().Route("/api", func(api *chain.Mux) { api.HandleFunc("GET", echo("")) })
				},
				want: "chain: pattern GET has no path to prefix",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) { expectPanic(t, tt.want, tt.fn) })
		}
	})
}

func TestMux_WithNotFound(t *testing.T) {
	custom := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Custom 404"))
	})

	t.Run("serves the handler for unmatched paths", func(t *testing.T) {
		mux := chain.New().WithNotFound(custom)
		if got := request(t, mux, http.MethodGet, "/missing"); got.status != 404 || got.body != "Custom 404" {
			t.Errorf("got %d %q, want 404 \"Custom 404\"", got.status, got.body)
		}
	})

	t.Run("nil restores the default", func(t *testing.T) {
		mux := chain.New().WithNotFound(custom).WithNotFound(nil)
		if got := request(t, mux, http.MethodGet, "/missing"); got.body != "404 page not found\n" {
			t.Errorf("body = %q, want the standard library's 404 body", got.body)
		}
	})

	t.Run("runs inside root middleware", func(t *testing.T) {
		var order []string
		var log []observed
		mux := chain.New().
			Use(inspect(&log)).
			Use(trace(&order, "mw")).
			WithNotFound(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "404")
				custom(w, r)
			}))

		request(t, mux, http.MethodGet, "/missing")

		if want := []string{"mw:before", "404", "mw:after"}; !reflect.DeepEqual(order, want) {
			t.Errorf("order = %v, want %v", order, want)
		}
		want := observed{http.MethodGet, "/missing", 404, len("Custom 404"), true}
		if len(log) != 1 || log[0] != want {
			t.Errorf("observed %+v, want [%+v]", log, want)
		}
	})

	t.Run("starts with a clean content type", func(t *testing.T) {
		mux := chain.New().WithNotFound(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ct := w.Header().Get("Content-Type"); ct != "" {
				t.Errorf("Content-Type already %q when the handler ran", ct)
			}
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("<!doctype html><h1>missing</h1>"))
		}))
		got := request(t, mux, http.MethodGet, "/missing")
		if ct := got.header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q, want sniffed text/html", ct)
		}
	})

	t.Run("does not intercept a handler's own 404", func(t *testing.T) {
		mux := chain.New().WithNotFound(custom).Use(setHeader("Access-Control-Allow-Origin", "*"))
		mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"error":"user %s not found"}`, r.PathValue("id"))
		})

		got := request(t, mux, http.MethodGet, "/users/42")
		if got.status != 404 || got.body != `{"error":"user 42 not found"}` {
			t.Errorf("got %d %q, want the handler's own 404 body", got.status, got.body)
		}
		if ct := got.header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		if got.header.Get("Access-Control-Allow-Origin") != "*" {
			t.Error("header set by middleware was lost")
		}
	})

	t.Run("returns the mux for chaining", func(t *testing.T) {
		mux := chain.New()
		if mux.WithNotFound(custom) != mux {
			t.Error("WithNotFound did not return the mux")
		}
	})

	t.Run("panics inside a child", func(t *testing.T) {
		const want = "chain: WithNotFound must be called on the root Mux, not inside Group or Route"
		t.Run("Group", func(t *testing.T) {
			expectPanic(t, want, func() { chain.New().Group(func(g *chain.Mux) { g.WithNotFound(custom) }) })
		})
		t.Run("Route", func(t *testing.T) {
			expectPanic(t, want, func() { chain.New().Route("/api", func(r *chain.Mux) { r.WithNotFound(custom) }) })
		})
	})
}

func TestMux_WithMethodNotAllowed(t *testing.T) {
	custom := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte("Custom 405"))
	})
	withRoutes := func(m *chain.Mux) *chain.Mux {
		return m.HandleFunc("GET /x", echo("get")).HandleFunc("PUT /x", echo("put"))
	}

	t.Run("serves the handler for a wrong method", func(t *testing.T) {
		mux := withRoutes(chain.New().WithMethodNotAllowed(custom))
		if got := request(t, mux, http.MethodPost, "/x"); got.status != 405 || got.body != "Custom 405" {
			t.Errorf("got %d %q, want 405 \"Custom 405\"", got.status, got.body)
		}
	})

	t.Run("nil restores the default", func(t *testing.T) {
		mux := withRoutes(chain.New().WithMethodNotAllowed(custom).WithMethodNotAllowed(nil))
		if got := request(t, mux, http.MethodPost, "/x"); got.body != "Method Not Allowed\n" {
			t.Errorf("body = %q, want the standard library's 405 body", got.body)
		}
	})

	t.Run("keeps the Allow header", func(t *testing.T) {
		var seen string
		mux := withRoutes(chain.New().WithMethodNotAllowed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = w.Header().Get("Allow")
			custom(w, r)
		})))

		got := request(t, mux, http.MethodPost, "/x")
		const want = "GET, HEAD, PUT"
		if seen != want {
			t.Errorf("Allow seen by handler = %q, want %q", seen, want)
		}
		if got.header.Get("Allow") != want {
			t.Errorf("Allow on the wire = %q, want %q", got.header.Get("Allow"), want)
		}
	})

	t.Run("runs inside root middleware", func(t *testing.T) {
		var log []observed
		mux := withRoutes(chain.New().Use(inspect(&log)).WithMethodNotAllowed(custom))

		request(t, mux, http.MethodPost, "/x")
		want := observed{http.MethodPost, "/x", 405, len("Custom 405"), true}
		if len(log) != 1 || log[0] != want {
			t.Errorf("observed %+v, want [%+v]", log, want)
		}
	})

	t.Run("does not intercept a handler's own 405", func(t *testing.T) {
		mux := chain.New().WithMethodNotAllowed(custom)
		mux.HandleFunc("GET /teapot", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "no", http.StatusMethodNotAllowed)
		})
		if got := request(t, mux, http.MethodGet, "/teapot"); got.status != 405 || got.body != "no\n" {
			t.Errorf("got %d %q, want the handler's own 405 body", got.status, got.body)
		}
	})

	t.Run("returns the mux for chaining", func(t *testing.T) {
		mux := chain.New()
		if mux.WithMethodNotAllowed(custom) != mux {
			t.Error("WithMethodNotAllowed did not return the mux")
		}
	})

	t.Run("panics inside a child", func(t *testing.T) {
		const want = "chain: WithMethodNotAllowed must be called on the root Mux, not inside Group or Route"
		t.Run("Group", func(t *testing.T) {
			expectPanic(t, want, func() { chain.New().Group(func(g *chain.Mux) { g.WithMethodNotAllowed(custom) }) })
		})
		t.Run("Route", func(t *testing.T) {
			expectPanic(t, want, func() { chain.New().Route("/api", func(r *chain.Mux) { r.WithMethodNotAllowed(custom) }) })
		})
	})
}

func TestMux_ServeHTTP(t *testing.T) {
	t.Run("root middleware runs for every request", func(t *testing.T) {
		var log []observed
		var groupSeen []string
		mux := chain.New().Use(inspect(&log))
		mux.HandleFunc("GET /ok", echo("ok"))
		mux.HandleFunc("GET /dir/", echo("dir"))
		mux.Group(func(g *chain.Mux) {
			g.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					groupSeen = append(groupSeen, r.URL.Path)
					next.ServeHTTP(w, r)
				})
			})
			g.HandleFunc("GET /grouped", echo("grouped"))
		})

		// The trailing-slash redirect status depends on the Go version, so take
		// it from a plain ServeMux rather than hard-coding it.
		std := http.NewServeMux()
		std.HandleFunc("GET /dir/", echo("dir"))
		redirect := request(t, std, http.MethodGet, "/dir").status

		tests := []struct {
			method, path string
			status       int
		}{
			{http.MethodGet, "/ok", 200},
			{http.MethodGet, "/missing", 404},
			{http.MethodPost, "/ok", 405},
			{http.MethodGet, "/dir", redirect},
			{http.MethodGet, "/grouped", 200},
		}
		srv := serve(t, mux)
		for _, tt := range tests {
			if got := do(t, srv, tt.method, tt.path); got.status != tt.status {
				t.Errorf("%s %s: status = %d, want %d", tt.method, tt.path, got.status, tt.status)
			}
		}

		if len(log) != len(tests) {
			t.Fatalf("root middleware saw %d requests, want %d: %+v", len(log), len(tests), log)
		}
		for i, tt := range tests {
			got := log[i]
			if got.method != tt.method || got.path != tt.path || got.status != tt.status || got.size == 0 {
				t.Errorf("observed[%d] = %+v, want %s %s %d with a body", i, got, tt.method, tt.path, tt.status)
			}
		}
		if !reflect.DeepEqual(groupSeen, []string{"/grouped"}) {
			t.Errorf("group middleware saw %v, want only /grouped", groupSeen)
		}
	})

	t.Run("unmatched responses match the standard library", func(t *testing.T) {
		register := func(handle func(string, func(http.ResponseWriter, *http.Request))) {
			handle("GET /x", echo("get"))
			handle("PUT /x", echo("put"))
			handle("GET /dir/", echo("dir"))
		}
		passthrough := func(next http.Handler) http.Handler { return next }

		std := http.NewServeMux()
		register(std.HandleFunc)
		mux := chain.New().Use(passthrough, passthrough)
		register(func(p string, h func(http.ResponseWriter, *http.Request)) { mux.HandleFunc(p, h) })

		headers := []string{"Content-Type", "X-Content-Type-Options", "Allow", "Location", "Content-Length"}
		tests := []struct{ method, path string }{
			{http.MethodGet, "/missing"},
			{http.MethodPost, "/x"},
			{http.MethodGet, "/dir"},
			{http.MethodHead, "/missing"},
		}
		stdSrv, muxSrv := serve(t, std), serve(t, mux)
		for _, tt := range tests {
			t.Run(tt.method+" "+tt.path, func(t *testing.T) {
				want := do(t, stdSrv, tt.method, tt.path)
				got := do(t, muxSrv, tt.method, tt.path)
				if got.status != want.status || got.body != want.body {
					t.Errorf("got %d %q, stdlib sends %d %q", got.status, got.body, want.status, want.body)
				}
				for _, h := range headers {
					if got.header.Get(h) != want.header.Get(h) {
						t.Errorf("header %s = %q, stdlib sends %q", h, got.header.Get(h), want.header.Get(h))
					}
				}
			})
		}
	})

	t.Run("middleware can end an unmatched request", func(t *testing.T) {
		notFoundCalled := false
		mux := chain.New().
			Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
				})
			}).
			WithNotFound(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				notFoundCalled = true
			}))

		got := request(t, mux, http.MethodGet, "/missing")
		if got.status != http.StatusUnauthorized || got.body != "unauthorized\n" {
			t.Errorf("got %d %q, want the middleware's 401", got.status, got.body)
		}
		if notFoundCalled {
			t.Error("custom 404 handler ran although middleware ended the response")
		}
	})

	t.Run("exposes the response to middleware", func(t *testing.T) {
		tests := []struct {
			name    string
			handler http.HandlerFunc
			want    observed
		}{
			{
				name: "status and size",
				handler: func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
					w.Write([]byte("created"))
				},
				want: observed{http.MethodGet, "/", http.StatusCreated, len("created"), true},
			},
			{
				name:    "write without WriteHeader defaults to 200",
				handler: echo("ok"),
				want:    observed{http.MethodGet, "/", http.StatusOK, 2, true},
			},
			{
				name:    "nothing written",
				handler: func(w http.ResponseWriter, r *http.Request) {},
				want:    observed{http.MethodGet, "/", http.StatusOK, 0, false},
			},
			{
				name: "second WriteHeader is ignored",
				handler: func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusAccepted)
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte("test"))
				},
				want: observed{http.MethodGet, "/", http.StatusAccepted, 4, true},
			},
			{
				name: "large body",
				handler: func(w http.ResponseWriter, r *http.Request) {
					w.Write([]byte(strings.Repeat("A", 1<<20)))
				},
				want: observed{http.MethodGet, "/", http.StatusOK, 1 << 20, true},
			},
			{
				name: "flush marks the response written",
				handler: func(w http.ResponseWriter, r *http.Request) {
					w.(http.Flusher).Flush()
				},
				want: observed{http.MethodGet, "/", http.StatusOK, 0, true},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var log []observed
				mux := chain.New().Use(inspect(&log)).HandleFunc("GET /", tt.handler)
				got := request(t, mux, http.MethodGet, "/")
				if got.status != tt.want.status || len(got.body) != tt.want.size {
					t.Errorf("client got %d with %d bytes, want %d with %d bytes", got.status, len(got.body), tt.want.status, tt.want.size)
				}
				if len(log) != 1 || log[0] != tt.want {
					t.Errorf("observed %+v, want [%+v]", log, tt.want)
				}
			})
		}
	})

	t.Run("written is false before the handler runs", func(t *testing.T) {
		var before bool
		mux := chain.New().
			Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					before = w.(chain.ResponseWriter).Written()
					next.ServeHTTP(w, r)
				})
			}).
			HandleFunc("GET /", echo("ok"))
		request(t, mux, http.MethodGet, "/")
		if before {
			t.Error("Written() was true before the handler ran")
		}
	})

	t.Run("early hints do not finalise the status", func(t *testing.T) {
		var log []observed
		mux := chain.New().Use(inspect(&log)).HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Link", "</style.css>; rel=preload; as=style")
			w.WriteHeader(http.StatusEarlyHints)
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("nope"))
		})
		srv := serve(t, mux)

		var informational []int
		trace := &httptrace.ClientTrace{Got1xxResponse: func(code int, _ textproto.MIMEHeader) error {
			informational = append(informational, code)
			return nil
		}}
		ctx := httptrace.WithClientTrace(context.Background(), trace)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		if !reflect.DeepEqual(informational, []int{http.StatusEarlyHints}) {
			t.Errorf("client received 1xx responses %v, want [103]", informational)
		}
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("final status = %d, want 404", resp.StatusCode)
		}
		if len(log) != 1 || log[0].status != http.StatusNotFound {
			t.Errorf("observed %+v, want Status() 404", log)
		}
	})

	t.Run("writer implements the optional interfaces", func(t *testing.T) {
		var flusher, hijacker, pusher, unwrapper bool
		mux := chain.New().HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			_, flusher = w.(http.Flusher)
			_, hijacker = w.(http.Hijacker)
			_, pusher = w.(http.Pusher)
			u, ok := w.(interface{ Unwrap() http.ResponseWriter })
			unwrapper = ok && u.Unwrap() != nil
		})
		request(t, mux, http.MethodGet, "/")
		if !flusher || !hijacker || !pusher || !unwrapper {
			t.Errorf("Flusher=%v Hijacker=%v Pusher=%v Unwrap=%v, want all true", flusher, hijacker, pusher, unwrapper)
		}
	})

	t.Run("streams server-sent events incrementally", func(t *testing.T) {
		// The handler sends event 2 only after the client has read event 1, so
		// the test proves Flush delivers through the wrapper and middleware
		// rather than the body arriving all at once when the handler returns.
		clientGotFirst := make(chan struct{})
		mux := chain.New().Use(setHeader("X-Logged", "on")).HandleFunc("GET /sse", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte("data: event1\n\n"))
			w.(http.Flusher).Flush()
			select {
			case <-clientGotFirst:
			case <-time.After(2 * time.Second):
				t.Error("client never acknowledged the first event: Flush did not deliver")
				return
			}
			w.Write([]byte("data: event2\n\n"))
			w.(http.Flusher).Flush()
		})
		srv := serve(t, mux)

		resp, err := testClient.Get(srv.URL + "/sse")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
			t.Fatalf("Content-Type = %q", ct)
		}
		buf := make([]byte, 64)
		n, err := resp.Body.Read(buf)
		if err != nil || string(buf[:n]) != "data: event1\n\n" {
			t.Fatalf("first read = %q, %v; want the first event alone", buf[:n], err)
		}
		close(clientGotFirst)
		rest, err := io.ReadAll(resp.Body)
		if err != nil || string(rest) != "data: event2\n\n" {
			t.Fatalf("remaining body = %q, %v; want the second event", rest, err)
		}
	})

	t.Run("hijacks the connection", func(t *testing.T) {
		// Through a real net/http server, not a mock: after Hijack the handler
		// owns the socket and whatever it writes reaches the client verbatim.
		mux := chain.New().Use(setHeader("X-Logged", "on")).HandleFunc("GET /upgrade", func(w http.ResponseWriter, r *http.Request) {
			conn, rw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("Hijack: %v", err)
				return
			}
			defer conn.Close()
			rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\n\r\nraw bytes")
			rw.Flush()
		})
		srv := serve(t, mux)

		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		fmt.Fprint(conn, "GET /upgrade HTTP/1.1\r\nHost: x\r\n\r\n")
		got, err := io.ReadAll(conn)
		if err != nil {
			t.Fatal(err)
		}
		const want = "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\n\r\nraw bytes"
		if string(got) != want {
			t.Errorf("client received %q, want %q", got, want)
		}
	})

	t.Run("a panicking handler does not take down the server", func(t *testing.T) {
		mux := chain.New().
			Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/panic" {
						panic("middleware panic")
					}
					next.ServeHTTP(w, r)
				})
			}).
			HandleFunc("GET /panic", echo("unreachable")).
			HandleFunc("GET /ok", echo("ok"))
		srv := serve(t, mux)

		// net/http recovers the panic and closes the connection, so the client
		// sees an error. The server must still answer the next request.
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/panic", nil)
		if resp, err := testClient.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				t.Errorf("status = %d, want 5xx after a panic", resp.StatusCode)
			}
		}
		if got := do(t, srv, http.MethodGet, "/ok"); got.body != "ok" {
			t.Errorf("server did not recover: got %d %q", got.status, got.body)
		}
	})

	t.Run("handles concurrent requests", func(t *testing.T) {
		var mu sync.Mutex
		count := 0
		mux := chain.New().
			Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					count++
					mu.Unlock()
					next.ServeHTTP(w, r)
				})
			}).
			HandleFunc("GET /", echo("ok"))
		srv := serve(t, mux)

		const n = 10
		var wg sync.WaitGroup
		errs := make(chan error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, err := http.Get(srv.URL + "/")
				if err != nil {
					errs <- err
					return
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					errs <- fmt.Errorf("status = %d, want 200", resp.StatusCode)
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		if count != n {
			t.Errorf("middleware ran %d times, want %d", count, n)
		}
	})
}
