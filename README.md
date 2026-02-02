# Chain

A lightweight, flexible HTTP middleware chaining solution for Go

## Overview

Chain is a composable HTTP middleware router package that provides a chainable API for organizing your web application's routes and middleware. It is built on top of Go's standard `http.ServeMux`.

Chain is designed to work with Go 1.22's new routing enhancements, supporting HTTP method matching and path wildcards in the same pattern format as the standard library. This makes it easy to transition between standard Go HTTP servers and Chain's enhanced middleware capabilities.

Chain was inspired by [alexedwards/flow](https://github.com/alexedwards/flow/).

## Features

- **Middleware Chaining**: Easily add request/response processing middleware
- **Method Chaining API**: API for registering routes and middleware
- **Response Monitoring**: Optional response wrapper for tracking status codes and sizes
- **Route Grouping**: Group routes with their own isolated middleware stacks
- **Custom Error Handlers**: Define custom handlers for 404 Not Found and 405 Method Not Allowed responses
- **Go 1.22 Compatible**: Works with Go's new routing enhancements including method matching and path wildcards

## Installation

```bash
go get github.com/jpl-au/chain
```

## Basic Usage

```go
package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/jpl-au/chain"
)

func main() {
	// Create a new Chain router
	mux := chain.New()

	// Add global logging middleware
	mux.Use(loggingMiddleware)

	// Add routes
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Welcome to the home page!")
	})

	// Add authenticated routes in a group
	mux.Group(func(api *chain.Mux) {
		// This middleware only applies to routes in this group
		api.Use(authMiddleware)

		api.HandleFunc("GET /dashboard", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "Welcome to your dashboard!")
		})
	})

	// Start the server
	log.Println("Server starting on :8080")
	http.ListenAndServe(":8080", mux)
}

// Example logging middleware
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("Started %s %s", r.Method, r.URL.Path)
		
		next.ServeHTTP(w, r)
		
		log.Printf("Completed in %v", time.Since(start))
	})
}

// Example auth middleware
func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check authentication (simplified for example)
		if r.Header.Get("X-API-Key") == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		
		next.ServeHTTP(w, r)
	})
}
```

## Chaining API

Chain provides a fluent method chaining API for a more expressive syntax:

```go
mux := chain.New().
    Use(loggingMiddleware).
    Use(recoverMiddleware).
    HandleFunc("GET /", homeHandler).
    HandleFunc("POST /users", createUserHandler).
    HandleFunc("GET /users/{id}", getUserHandler)
```

Note that Chain follows Go 1.22's pattern matching rules, including method matching and path wildcards. You can access path parameters using `r.PathValue("id")` in your handlers.

## Path Wildcards and Parameters

Chain uses Go 1.22's path parameter syntax. Access path parameters in your handlers using `r.PathValue()`:

```go
// Match a specific path segment with a named parameter
mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
    // Get the id parameter value
    id := r.PathValue("id")
    fmt.Fprintf(w, "User ID: %s", id)
})

// Use trailing ... to capture all remaining path segments
mux.HandleFunc("GET /files/{path...}", func(w http.ResponseWriter, r *http.Request) {
    path := r.PathValue("path")
    fmt.Fprintf(w, "Requested file path: %s", path)
})

// The {$} pattern to match exact paths with trailing slashes
mux.HandleFunc("GET /posts/{$}", listPostsHandler)      // Matches ONLY "/posts/" exactly
mux.HandleFunc("GET /posts/{id}", getSpecificPostHandler) // Matches "/posts/123", etc.

// Routes with trailing slashes (like /posts/) match all paths with that prefix
// but {$} restricts to exact match
mux.HandleFunc("GET /api/", apiIndexHandler)           // Matches "/api/", "/api/users", etc.
mux.HandleFunc("GET /api/{$}", apiExactHandler)        // Matches ONLY "/api/" exactly
```

The `{$}` pattern is particularly useful when you need to distinguish between a collection endpoint (e.g., `/posts/`) and a specific resource endpoint (e.g., `/posts/{id}`).

## Response Wrapper

Enable the response wrapper to track status codes, response size, and more detailed logging:

```go
mux := chain.New().
	Use(advancedLoggingMiddleware)

// Advanced logging middleware with response information
func advancedLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		next.ServeHTTP(w, r)

		// Access response information after handler execution
		if rw, ok := w.(chain.ResponseWriter); ok {
			log.Printf(
				"%s %s %d %d %v",
				r.Method,
				r.URL.Path,
				rw.Status(),
				rw.Size(),
				time.Since(start),
			)
		}
	})
}
```

### Advanced ResponseWriter Features

Chain's response wrapper implements standard HTTP interfaces, enabling support for:

- **Server-Sent Events (SSE)**: Implements `http.Flusher` for streaming responses
- **WebSockets**: Implements `http.Hijacker` for connection upgrades
- **HTTP/2 Server Push**: Implements `http.Pusher` for push promises
- **ResponseController**: Implements `Unwrap()` for `http.ResponseController` compatibility

These interfaces are automatically delegated to the underlying `http.ResponseWriter` when supported, making Chain compatible with SSE, WebSockets, HTTP/2 push, and other advanced HTTP features.

## Custom Error Handlers

Set custom handlers for 404 Not Found and 405 Method Not Allowed responses. The response wrapper intercepts these status codes to execute your custom handlers:

```go
// Using named handler functions
mux := chain.New().
	WithNotFound(http.HandlerFunc(customNotFoundHandler)).
	WithMethodNotAllowed(http.HandlerFunc(customMethodNotAllowedHandler))

func customNotFoundHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(w, "Sorry, the page %s was not found", r.URL.Path)
}

func customMethodNotAllowedHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusMethodNotAllowed)
	fmt.Fprintf(w, "Method %s is not allowed for %s", r.Method, r.URL.Path)
}

// Using inline anonymous functions
mux := chain.New().
	WithNotFound(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "Sorry, the page %s was not found", r.URL.Path)
	})).
	WithMethodNotAllowed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		fmt.Fprintf(w, "Method %s is not allowed for %s", r.Method, r.URL.Path)
	}))
```

## Route Grouping

Group related routes and apply middleware only to those routes:

```go
// API routes with authentication
mux.Group(func(api *chain.Mux) {
    // Auth middleware only applies to these routes
    api.Use(authMiddleware)

    api.HandleFunc("GET /api/users", listUsersHandler)
    api.HandleFunc("POST /api/users", createUserHandler)

    // Nested group for admin-only routes
    api.Group(func(admin *chain.Mux) {
        admin.Use(adminOnlyMiddleware)

        admin.HandleFunc("DELETE /api/users/{id}", deleteUserHandler)
    })
})
```

Groups are useful for organizing routes by functionality, applying specific middleware to sets of routes, and creating clear hierarchies in your application structure.

## Route Prefixes

Use `Route` to create groups with a path prefix. All routes registered within the group will have the prefix prepended automatically:

```go
mux.Route("/api/v1", func(api *chain.Mux) {
    api.Use(authMiddleware)

    api.HandleFunc("GET /users", listUsersHandler)      // Registers "GET /api/v1/users"
    api.HandleFunc("GET /users/{id}", getUserHandler)   // Registers "GET /api/v1/users/{id}"
    api.HandleFunc("POST /users", createUserHandler)    // Registers "POST /api/v1/users"
})
```

Prefixes can be nested for versioned APIs or complex route hierarchies:

```go
mux.Route("/api", func(api *chain.Mux) {
    api.Route("/v1", func(v1 *chain.Mux) {
        v1.HandleFunc("GET /users", listUsersV1)  // Registers "GET /api/v1/users"
    })
    api.Route("/v2", func(v2 *chain.Mux) {
        v2.HandleFunc("GET /users", listUsersV2)  // Registers "GET /api/v2/users"
    })
})
```

`Group` and `Route` can be combined - groups inside a route inherit the prefix:

```go
mux.Route("/api", func(api *chain.Mux) {
    api.Group(func(authed *chain.Mux) {
        authed.Use(authMiddleware)
        authed.HandleFunc("GET /secret", secretHandler)  // Registers "GET /api/secret"
    })
})
```

## Important Notes

* Chain uses Go 1.22's standard pattern matching rules and precedence, so more specific patterns take precedence over more general ones.
* Path parameters are accessed via Go 1.22's standard `r.PathValue("paramName")` method.
* Middleware is applied in the order it's registered, with innermost middleware executed first.
* Middleware should be registered before the routes it needs to affect.
* Route groups create isolated middleware stacks that include parent middleware.
* The response wrapper is always enabled, providing access to `Status()` and `Size()` in middleware.

## License

MIT License

## Learn More

For more information about Go 1.22's routing enhancements that Chain builds upon, see the [official Go blog post](https://go.dev/blog/routing-enhancements).

## Acknowledgments

Chain draws inspiration from [alexedwards/flow](https://github.com/alexedwards/flow/), a minimal HTTP router for Go. While Flow uses its own pattern matching syntax, Chain adopts Go 1.22's standard library approach for routing patterns, making it a natural choice for projects using Go 1.22+.