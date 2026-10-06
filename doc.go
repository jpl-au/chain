// Package chain provides a lightweight, flexible HTTP middleware chaining solution for Go.
//
// Chain is a composable HTTP middleware router that provides a chainable API for organizing
// routes and middleware. It is built on top of Go's standard [http.ServeMux] and uses its
// routing, including HTTP method matching and path wildcards.
//
// # Basic Usage
//
// Create a new router, add middleware, and register routes:
//
//	mux := chain.New()
//	mux.Use(loggingMiddleware)
//	mux.HandleFunc("GET /users/{id}", getUserHandler)
//	http.ListenAndServe(":8080", mux)
//
// # Middleware
//
// Middleware are functions that wrap an [http.Handler] and return an [http.Handler].
// They are executed in the order they are registered:
//
//	mux.Use(firstMiddleware)   // Runs first (outermost)
//	mux.Use(secondMiddleware)  // Runs second (innermost)
//
// # Route Groups
//
// Groups allow middleware to be scoped to a subset of routes:
//
//	mux.Group(func(api *chain.Mux) {
//		api.Use(authMiddleware) // Only applies to routes in this group
//		api.HandleFunc("GET /api/users", listUsersHandler)
//	})
//
// # Route Prefixes
//
// Use [Mux.Route] to create groups with a path prefix. All routes registered within
// the group will have the prefix prepended automatically:
//
//	mux.Route("/api/v1", func(api *chain.Mux) {
//		api.Use(authMiddleware)
//		api.HandleFunc("GET /users", listUsersHandler)     // Registers "GET /api/v1/users"
//		api.HandleFunc("GET /users/{id}", getUserHandler)  // Registers "GET /api/v1/users/{id}"
//	})
//
// Prefixes can be nested:
//
//	mux.Route("/api", func(api *chain.Mux) {
//		api.Route("/v1", func(v1 *chain.Mux) {
//			v1.HandleFunc("GET /users", listUsersHandler)  // Registers "GET /api/v1/users"
//		})
//	})
//
// # Response Wrapper
//
// Chain wraps all responses with a [ResponseWriter] that tracks the status code and
// response size. Middleware obtains it with [Writer] after the handler executes:
//
//	func loggingMiddleware(next http.Handler) http.Handler {
//		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//			next.ServeHTTP(w, r)
//			if rw, ok := chain.Writer(w); ok {
//				log.Printf("%s %s %d", r.Method, r.URL.Path, rw.Status())
//			}
//		})
//	}
//
// [Writer] looks through other wrappers that implement Unwrap() http.ResponseWriter,
// so it still finds the writer when gzip or tracing middleware sits in between.
// [ResponseWriter.Status] is 200 until a status is written; use [ResponseWriter.Written]
// to tell an unwritten response from a 200.
//
// The response wrapper also implements [http.Flusher], [http.Hijacker], and [http.Pusher]
// for compatibility with SSE, WebSockets, and HTTP/2 server push.
//
// # Custom Error Handlers
//
// Custom handlers can be set for 404 Not Found and 405 Method Not Allowed responses:
//
//	mux := chain.New().
//		WithNotFound(notFoundHandler).
//		WithMethodNotAllowed(methodNotAllowedHandler)
//
// They run only when the router itself cannot route a request. A 404 or 405
// written by one of your own handlers is never intercepted. Custom handlers
// run inside the root middleware chain, and on a 405 the Allow header is
// already set.
//
// # Unmatched Requests
//
// Root middleware runs for every request, including those that match no route.
// The 404, 405 and trailing-slash redirect responses the standard library
// generates for such requests pass through the root middleware unchanged when
// no custom handler is set. Group middleware does not run for them, since an
// unmatched request belongs to no group.
//
// # Path Parameters
//
// Path parameters use the standard library's syntax and are accessed via [http.Request.PathValue]:
//
//	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
//		id := r.PathValue("id")
//		// ...
//	})
package chain
