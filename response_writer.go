package chain

import (
	"bufio"
	"net"
	"net/http"
)

// responseWriter wraps http.ResponseWriter and tracks response status and size.
// It implements http.Flusher, http.Hijacker, and http.Pusher by delegating to
// the underlying ResponseWriter when supported.
type responseWriter struct {
	http.ResponseWriter
	status  int
	size    int
	written bool

	// Interception
	req              *http.Request
	notFound         http.Handler
	methodNotAllowed http.Handler
	ignoreWrites     bool
}

// Compile-time interface checks
var (
	_ http.ResponseWriter = (*responseWriter)(nil)
	_ http.Flusher        = (*responseWriter)(nil)
	_ http.Hijacker       = (*responseWriter)(nil)
	_ http.Pusher         = (*responseWriter)(nil)
	_ ResponseWriter      = (*responseWriter)(nil)
)

// Status returns the HTTP status code of the response. If not yet written, it returns 200 OK.
func (rw *responseWriter) Status() int {
	if rw.status == 0 {
		return http.StatusOK
	}
	return rw.status
}

// Size returns the number of bytes written to the response.
func (rw *responseWriter) Size() int {
	return rw.size
}

// Written returns whether the response has been written to.
func (rw *responseWriter) Written() bool {
	return rw.written
}

// WriteHeader sends an HTTP response header with the provided status code.
func (rw *responseWriter) WriteHeader(status int) {
	if rw.written {
		return
	}

	// Check for interception (only on first write, before status is set)
	if rw.status == 0 {
		if status == http.StatusNotFound && rw.notFound != nil {
			rw.handleInterception(rw.notFound)
			return
		}
		if status == http.StatusMethodNotAllowed && rw.methodNotAllowed != nil {
			rw.handleInterception(rw.methodNotAllowed)
			return
		}
	}

	rw.status = status
	rw.written = true
	rw.ResponseWriter.WriteHeader(status)
}

func (rw *responseWriter) handleInterception(handler http.Handler) {
	// Prevent infinite recursion by clearing handlers
	rw.notFound = nil
	rw.methodNotAllowed = nil

	// Clear headers set by the original handler (e.g. ServeMux sets Content-Type)
	// so the custom handler has a clean slate
	h := rw.ResponseWriter.Header()
	for k := range h {
		delete(h, k)
	}

	handler.ServeHTTP(rw, rw.req)

	// The original handler (ServeMux) will continue writing its default response
	// after we return, so we need to discard those writes
	rw.ignoreWrites = true
}

// Write writes the data to the connection as part of an HTTP reply.
func (rw *responseWriter) Write(b []byte) (int, error) {
	if rw.ignoreWrites {
		return len(b), nil
	}
	if !rw.written {
		rw.written = true
		rw.status = http.StatusOK
	}
	size, err := rw.ResponseWriter.Write(b)
	rw.size += size
	return size, err
}

// Unwrap returns the underlying http.ResponseWriter.
// This enables http.ResponseController to access the original ResponseWriter.
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// Flush implements http.Flusher.
// Sends any buffered data to the client.
func (rw *responseWriter) Flush() {
	http.NewResponseController(rw.ResponseWriter).Flush()
}

// Hijack implements http.Hijacker.
// Allows the caller to take over the connection.
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(rw.ResponseWriter).Hijack()
}

// Push implements http.Pusher.
// Initiates an HTTP/2 server push.
func (rw *responseWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := rw.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

// wrapResponseWriter wraps an http.ResponseWriter.
func wrapResponseWriter(w http.ResponseWriter, r *http.Request, notFound, methodNotAllowed http.Handler) ResponseWriter {
	return &responseWriter{
		ResponseWriter:   w,
		req:              r,
		notFound:         notFound,
		methodNotAllowed: methodNotAllowed,
	}
}
