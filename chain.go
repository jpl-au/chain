package chain

import (
	"net/http"
	"strings"
)

// ResponseWriter extends http.ResponseWriter with additional methods to inspect the response.
// Middleware obtains it with [Writer]. It always satisfies http.Flusher, http.Hijacker, and http.Pusher, delegating to the
// underlying ResponseWriter when it supports them. When it does not, Flush is a no-op
// and Hijack and Push return http.ErrNotSupported.
type ResponseWriter interface {
	http.ResponseWriter
	// Status returns the HTTP status code of the response. It is 200 until a
	// status has been written, so check Written to tell a 200 response from
	// one that has not been written at all.
	Status() int
	// Size returns the number of bytes written to the response.
	Size() int
	// Written returns whether the response has been written to, or the
	// connection hijacked.
	Written() bool
}

// Writer returns the ResponseWriter that Mux wrapped around w, looking through
// any intermediate wrappers that implement Unwrap() http.ResponseWriter, as
// gzip, CORS and tracing middleware commonly do. It reports false when the
// request did not pass through a Mux or when a wrapper in the way does not
// implement Unwrap.
//
// Use it in middleware to read the response after the handler has run:
//
//	next.ServeHTTP(w, r)
//	if rw, ok := chain.Writer(w); ok {
//		log.Printf("%d %d", rw.Status(), rw.Size())
//	}
func Writer(w http.ResponseWriter) (ResponseWriter, bool) {
	for w != nil {
		if rw, ok := w.(ResponseWriter); ok {
			return rw, true
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil, false
		}
		w = u.Unwrap()
	}
	return nil, false
}

// Mux is an HTTP request multiplexer with support for middleware chaining.
// It extends the standard http.ServeMux with features for applying middleware
// to groups of routes or to the entire router.
//
// Routes and middleware must be registered before the Mux starts serving
// requests; registration is not safe to perform concurrently with ServeHTTP.
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
//
// The handler is invoked only when no registered route matches the request.
// Responses with a 404 status written by your own handlers are left untouched.
// The handler runs inside the root middleware chain. Returns the Mux instance
// for chaining.
func (m *Mux) WithNotFound(handler http.Handler) *Mux {
	m.notFound = handler
	return m
}

// WithMethodNotAllowed sets a custom handler for 405 Method Not Allowed responses.
//
// The handler is invoked only when a route matches the request path but not its
// method. The Allow header listing the permitted methods is already set when the
// handler runs. Responses with a 405 status written by your own handlers are left
// untouched. The handler runs inside the root middleware chain. Returns the Mux
// instance for chaining.
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
	fn(m.child(""))
	return m
}

// Route creates a new routing group with a path prefix and isolated middleware.
// All routes registered within fn will have the prefix prepended to their patterns.
// Prefixes can be nested - a Route inside another Route will combine the prefixes.
//
// The prefix must begin with "/". A trailing "/" is ignored, so "/api/" and
// "/api" are equivalent. Returns the original Mux instance for method chaining.
func (m *Mux) Route(prefix string, fn func(*Mux)) *Mux {
	if fn == nil {
		panic("chain: nil function passed to Route")
	}
	if prefix != "" && prefix[0] != '/' {
		panic("chain: route prefix " + prefix + " must begin with /")
	}
	fn(m.child(strings.TrimSuffix(prefix, "/")))
	return m
}

// child returns a Mux sharing the router with a copy of the middleware chain
// and the given prefix appended.
func (m *Mux) child(prefix string) *Mux {
	return &Mux{
		router:      m.router,
		middlewares: append([]func(http.Handler) http.Handler{}, m.middlewares...),
		prefix:      m.prefix + prefix,
	}
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
// ServeMux patterns are "[METHOD ][HOST]/[PATH]", so the path starts at the
// first "/" and the prefix is inserted there.
func (m *Mux) prefixPattern(pattern string) string {
	if m.prefix == "" {
		return pattern
	}
	i := strings.IndexByte(pattern, '/')
	if i < 0 {
		panic("chain: pattern " + pattern + " has no path to prefix")
	}
	return pattern[:i] + m.prefix + pattern[i:]
}

// ServeHTTP dispatches the request to the handler whose pattern most closely
// matches the request URL.
//
// Every response passes through the root middleware chain, including the 404,
// 405 and trailing-slash redirect responses the underlying ServeMux generates
// when no route matches. Those are captured by the response wrapper and then
// served through the middleware, dispatching to the handlers set with
// WithNotFound and WithMethodNotAllowed, or replaying the standard library's
// response unchanged when none is set.
func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rw := wrapResponseWriter(w)
	rw.capturing = true
	m.router.ServeHTTP(rw, r)
	if rw.captured != 0 {
		m.serveUnrouted(rw, r)
	}
}

// serveUnrouted serves a response the ServeMux generated for a request that
// matched no route, running it through the root middleware chain.
func (m *Mux) serveUnrouted(rw *responseWriter, r *http.Request) {
	status, body := rw.captured, rw.capturedBody
	rw.captured, rw.capturedBody = 0, nil

	var h http.Handler
	custom := false
	switch {
	case status == http.StatusNotFound && m.notFound != nil:
		h, custom = m.notFound, true
	case status == http.StatusMethodNotAllowed && m.methodNotAllowed != nil:
		h, custom = m.methodNotAllowed, true
	default:
		// Replay the standard library's response exactly as it would have sent it.
		h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			w.Write(body)
		})
	}
	if custom {
		// Give the custom handler a clean slate: drop the headers http.Error set
		// for its text/plain body. Everything else, including Allow on a 405,
		// is preserved.
		rw.Header().Del("Content-Type")
		rw.Header().Del("X-Content-Type-Options")
	}
	m.wrap(h).ServeHTTP(rw, r)
}

// wrap applies the middleware chain to a http.Handler.
func (m *Mux) wrap(handler http.Handler) http.Handler {
	// Apply middleware in reverse order so first-registered runs outermost
	// (first to see request, last to see response)
	for i := len(m.middlewares) - 1; i >= 0; i-- {
		handler = m.middlewares[i](handler)
	}

	// Stop capturing before any middleware runs so the wrapper can tell
	// user-written responses apart from ones the ServeMux generated.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rw, ok := w.(*responseWriter); ok {
			rw.capturing = false
		}
		handler.ServeHTTP(w, r)
	})
}
