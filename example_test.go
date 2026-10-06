package chain_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/jpl-au/chain"
)

// send dispatches one request to h and returns the recorded response.
func send(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func ExampleNew() {
	mux := chain.New()

	mux.HandleFunc("GET /hello/{name}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "hello, %s", r.PathValue("name"))
	})

	rec := send(mux, http.MethodGet, "/hello/world")
	fmt.Println(rec.Code, rec.Body.String())
	// Output:
	// 200 hello, world
}

func ExampleMux_Use() {
	logging := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Println("logging: before")
			next.ServeHTTP(w, r)
			fmt.Println("logging: after")
		})
	}
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Println("auth: before")
			next.ServeHTTP(w, r)
			fmt.Println("auth: after")
		})
	}

	// The first middleware registered is the outermost.
	mux := chain.New().
		Use(logging).
		Use(auth).
		HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			fmt.Println("handler")
		})

	send(mux, http.MethodGet, "/")
	// Output:
	// logging: before
	// auth: before
	// handler
	// auth: after
	// logging: after
}

func ExampleMux_Group() {
	requireKey := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-API-Key") == "" {
				http.Error(w, "missing api key", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	mux := chain.New()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "public")
	})

	// Middleware registered inside the group applies only to the group's routes.
	mux.Group(func(g *chain.Mux) {
		g.Use(requireKey)
		g.HandleFunc("GET /account", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "private")
		})
	})

	fmt.Println(send(mux, http.MethodGet, "/").Code)
	fmt.Println(send(mux, http.MethodGet, "/account").Code)
	// Output:
	// 200
	// 401
}

func ExampleMux_Route() {
	mux := chain.New()

	// Every pattern registered inside the function gets the prefix.
	mux.Route("/api/v1", func(api *chain.Mux) {
		api.HandleFunc("GET /users", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "users")
		})
		api.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "user ", r.PathValue("id"))
		})
	})

	fmt.Println(send(mux, http.MethodGet, "/api/v1/users").Body)
	fmt.Println(send(mux, http.MethodGet, "/api/v1/users/42").Body)
	fmt.Println(send(mux, http.MethodGet, "/users").Code)
	// Output:
	// users
	// user 42
	// 404
}

func ExampleMux_WithNotFound() {
	mux := chain.New().
		WithNotFound(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "no route for %s", r.URL.Path)
		}))

	// A handler that writes its own 404 is left alone.
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no such user", http.StatusNotFound)
	})

	fmt.Println(send(mux, http.MethodGet, "/missing").Body)
	fmt.Println(strings.TrimSpace(send(mux, http.MethodGet, "/users/42").Body.String()))
	// Output:
	// no route for /missing
	// no such user
}

func ExampleMux_WithMethodNotAllowed() {
	mux := chain.New().
		WithMethodNotAllowed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusMethodNotAllowed)
			fmt.Fprintf(w, "%s not allowed, try %s", r.Method, w.Header().Get("Allow"))
		}))
	mux.HandleFunc("GET /users", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("PUT /users", func(w http.ResponseWriter, r *http.Request) {})

	rec := send(mux, http.MethodDelete, "/users")
	fmt.Println(rec.Code, rec.Body.String())
	// Output:
	// 405 DELETE not allowed, try GET, HEAD, PUT
}

func ExampleWriter() {
	logging := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			if rw, ok := chain.Writer(w); ok {
				fmt.Printf("%s %s -> %d (%d bytes)\n", r.Method, r.URL.Path, rw.Status(), rw.Size())
			}
		})
	}

	mux := chain.New().Use(logging)
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello")
	})
	mux.HandleFunc("POST /items", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	// Root middleware also sees requests that match no route.
	send(mux, http.MethodGet, "/hello")
	send(mux, http.MethodPost, "/items")
	send(mux, http.MethodGet, "/missing")
	// Output:
	// GET /hello -> 200 (5 bytes)
	// POST /items -> 201 (0 bytes)
	// GET /missing -> 404 (19 bytes)
}
