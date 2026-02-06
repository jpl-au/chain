package chain

import (
	"net/http"
)

// ResponseWriter extends http.ResponseWriter with additional methods to inspect the response.
// It also implements http.Flusher, http.Hijacker, and http.Pusher when the underlying
// ResponseWriter supports these interfaces.
type ResponseWriter interface {
	http.ResponseWriter
	// Status returns the HTTP status code of the response.
	Status() int
	// Size returns the number of bytes written to the response.
	Size() int
	// Written returns whether the response has been written to.
	Written() bool
}

// Mux is an HTTP request multiplexer with support for middleware chaining.
// It extends the standard http.ServeMux with features for applying middleware
// to groups of routes or to the entire router.
type Mux struct {
	router           *http.ServeMux
	middlewares      []func(http.Handler) http.Handler
	prefix           string
	notFound         http.Handler
	methodNotAllowed http.Handler
}

// New returns a new, initialized Mux instance.
func New() *Mux {
	return &Mux{
		router: http.NewServeMux(),
	}
}

// WithNotFound sets a custom handler for 404 Not Found responses.
// Automatically enables the response wrapper. Returns the Mux instance for chaining.
func (m *Mux) WithNotFound(handler http.Handler) *Mux {
	m.notFound = handler
	return m
}

// WithMethodNotAllowed sets a custom handler for 405 Method Not Allowed responses.
// Automatically enables the response wrapper. Returns the Mux instance for chaining.
func (m *Mux) WithMethodNotAllowed(handler http.Handler) *Mux {
	m.methodNotAllowed = handler
	return m
}

// Use appends middleware to the Mux's middleware chain.
// Middleware are executed in the order they are added.
// Returns the Mux instance for method chaining.
func (m *Mux) Use(mw ...func(http.Handler) http.Handler) *Mux {
	for _, fn := range mw {
		if fn == nil {
			panic("chain: nil middleware passed to Use")
		}
	}
	m.middlewares = append(m.middlewares, mw...)
	return m
}

// Group creates a new routing group with isolated middleware.
// Middleware registered within fn will only apply to routes defined within that group.
// The group inherits the parent's route prefix if one was set via Route.
// Returns the original Mux instance for method chaining.
func (m *Mux) Group(fn func(*Mux)) *Mux {
	if fn == nil {
		panic("chain: nil function passed to Group")
	}
	groupMux := &Mux{
		router:      m.router,
		middlewares: append([]func(http.Handler) http.Handler{}, m.middlewares...),
		prefix:      m.prefix,
	}
	fn(groupMux)
	return m
}

// Route creates a new routing group with a path prefix and isolated middleware.
// All routes registered within fn will have the prefix prepended to their patterns.
// Prefixes can be nested - a Route inside another Route will combine the prefixes.
// Returns the original Mux instance for method chaining.
func (m *Mux) Route(prefix string, fn func(*Mux)) *Mux {
	if fn == nil {
		panic("chain: nil function passed to Route")
	}
	groupMux := &Mux{
		router:      m.router,
		middlewares: append([]func(http.Handler) http.Handler{}, m.middlewares...),
		prefix:      m.prefix + prefix,
	}
	fn(groupMux)
	return m
}

// Handle registers a handler for the given pattern with middleware applied.
// If a route prefix is set (via Route), it will be prepended to the pattern's path.
// Returns the Mux instance for method chaining.
func (m *Mux) Handle(pattern string, handler http.Handler) *Mux {
	if handler == nil {
		panic("chain: nil handler passed to Handle")
	}
	m.router.Handle(m.prefixPattern(pattern), m.wrap(handler))
	return m
}

// HandleFunc registers a handler function for the given pattern with middleware applied.
// If a route prefix is set (via Route), it will be prepended to the pattern's path.
// Returns the Mux instance for method chaining.
func (m *Mux) HandleFunc(pattern string, handlerFunc http.HandlerFunc) *Mux {
	if handlerFunc == nil {
		panic("chain: nil handler passed to HandleFunc")
	}
	m.router.Handle(m.prefixPattern(pattern), m.wrap(handlerFunc))
	return m
}

// prefixPattern prepends the Mux's prefix to the pattern's path component.
// Go 1.22 patterns can be "/path" or "METHOD /path", so we find the "/" to locate
// where the path starts and insert the prefix there.
func (m *Mux) prefixPattern(pattern string) string {
	if m.prefix == "" {
		return pattern
	}

	// Find the path component (starts at first "/")
	pathStart := 0
	for i, c := range pattern {
		if c == '/' {
			pathStart = i
			break
		}
	}

	return pattern[:pathStart] + m.prefix + pattern[pathStart:]
}

// ServeHTTP dispatches the request to the handler whose pattern most closely matches the request URL.
// It also handles custom 404 and 405 logic if configured.
func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Normal path with potential interception in the wrapper
	m.router.ServeHTTP(m.wrapWriter(w, r), r)
}

// wrapWriter wraps the http.ResponseWriter.
func (m *Mux) wrapWriter(w http.ResponseWriter, r *http.Request) http.ResponseWriter {
	return wrapResponseWriter(w, r, m.notFound, m.methodNotAllowed)
}

// wrap applies the middleware chain to a http.Handler.
func (m *Mux) wrap(handler http.Handler) http.Handler {
	// Apply middleware in reverse order so first-registered runs outermost
	// (first to see request, last to see response)
	for i := len(m.middlewares) - 1; i >= 0; i-- {
		handler = m.middlewares[i](handler)
	}

	// Return a handler that provides the right ResponseWriter to middleware
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If this is being called from ServeHTTP, w is already the wrapped writer
		// If this is being called normally, we need to check if wrapping is needed

		// Check if w is already our ResponseWriter interface
		if _, ok := w.(ResponseWriter); !ok {
			// Not wrapped yet, wrap it now
			w = wrapResponseWriter(w, r, m.notFound, m.methodNotAllowed)
		}

		handler.ServeHTTP(w, r)
	})
}
