package chain

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockResponseWriter is a basic ResponseWriter that doesn't implement any optional interfaces
type mockResponseWriter struct {
	headers    http.Header
	statusCode int
	body       []byte
}

func newMockResponseWriter() *mockResponseWriter {
	return &mockResponseWriter{
		headers: make(http.Header),
	}
}

func (m *mockResponseWriter) Header() http.Header {
	return m.headers
}

func (m *mockResponseWriter) Write(b []byte) (int, error) {
	m.body = append(m.body, b...)
	return len(b), nil
}

func (m *mockResponseWriter) WriteHeader(statusCode int) {
	m.statusCode = statusCode
}

// mockFlusherWriter implements http.Flusher
type mockFlusherWriter struct {
	*mockResponseWriter
	flushCalled bool
}

func (m *mockFlusherWriter) Flush() {
	m.flushCalled = true
}

// mockHijackerWriter implements http.Hijacker
type mockHijackerWriter struct {
	*mockResponseWriter
	hijackCalled bool
}

func (m *mockHijackerWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	m.hijackCalled = true
	return nil, nil, errors.New("mock hijack")
}

// mockPusherWriter implements http.Pusher
type mockPusherWriter struct {
	*mockResponseWriter
	pushCalled bool
	pushTarget string
}

func (m *mockPusherWriter) Push(target string, opts *http.PushOptions) error {
	m.pushCalled = true
	m.pushTarget = target
	return nil
}

// mockFullWriter implements all three interfaces
type mockFullWriter struct {
	*mockResponseWriter
	flushCalled  bool
	hijackCalled bool
	pushCalled   bool
	pushTarget   string
}

func (m *mockFullWriter) Flush() {
	m.flushCalled = true
}

func (m *mockFullWriter) Push(target string, opts *http.PushOptions) error {
	m.pushCalled = true
	m.pushTarget = target
	return nil
}

func (m *mockFullWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	m.hijackCalled = true
	return nil, nil, errors.New("mock hijack")
}

func TestResponseWriter_BasicFunctionality(t *testing.T) {
	mock := newMockResponseWriter()
	rw := wrapResponseWriter(mock, nil, nil, nil)

	// Test Status() before writing
	if rw.Status() != http.StatusOK {
		t.Errorf("Expected default status 200, got %d", rw.Status())
	}

	// Test Written() before writing
	if rw.Written() {
		t.Error("Written() should be false before any writes")
	}

	// Test Size() before writing
	if rw.Size() != 0 {
		t.Errorf("Expected size 0, got %d", rw.Size())
	}

	// Write header
	rw.WriteHeader(http.StatusCreated)

	// Test Status() after WriteHeader
	if rw.Status() != http.StatusCreated {
		t.Errorf("Expected status 201, got %d", rw.Status())
	}

	// Test Written() after WriteHeader
	if !rw.Written() {
		t.Error("Written() should be true after WriteHeader")
	}

	// Write body
	content := []byte("test content")
	n, err := rw.Write(content)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Test Size() after Write
	if rw.Size() != len(content) {
		t.Errorf("Expected size %d, got %d", len(content), rw.Size())
	}

	if n != len(content) {
		t.Errorf("Expected to write %d bytes, got %d", len(content), n)
	}

	// Test that underlying writer received the data
	if mock.statusCode != http.StatusCreated {
		t.Errorf("Underlying writer has wrong status: %d", mock.statusCode)
	}

	if string(mock.body) != string(content) {
		t.Errorf("Underlying writer has wrong body: %s", mock.body)
	}
}

func TestResponseWriter_WriteWithoutHeader(t *testing.T) {
	mock := newMockResponseWriter()
	rw := wrapResponseWriter(mock, nil, nil, nil)

	// Write without calling WriteHeader first
	rw.Write([]byte("test"))

	// Should default to 200 OK
	if rw.Status() != http.StatusOK {
		t.Errorf("Expected default status 200, got %d", rw.Status())
	}

	if !rw.Written() {
		t.Error("Written() should be true after Write")
	}
}

func TestResponseWriter_DoubleWriteHeader(t *testing.T) {
	mock := newMockResponseWriter()
	rw := wrapResponseWriter(mock, nil, nil, nil)

	rw.WriteHeader(http.StatusAccepted)
	rw.WriteHeader(http.StatusBadRequest) // Second call should be ignored

	if rw.Status() != http.StatusAccepted {
		t.Errorf("Expected first status 202, got %d", rw.Status())
	}

	if mock.statusCode != http.StatusAccepted {
		t.Errorf("Underlying writer has wrong status: %d", mock.statusCode)
	}
}

func TestResponseWriter_Unwrap(t *testing.T) {
	mock := newMockResponseWriter()
	rw := wrapResponseWriter(mock, nil, nil, nil)

	// Cast to the concrete type to access Unwrap
	if unwrapper, ok := rw.(interface{ Unwrap() http.ResponseWriter }); ok {
		unwrapped := unwrapper.Unwrap()
		if unwrapped != mock {
			t.Error("Unwrap() should return the original ResponseWriter")
		}
	} else {
		t.Error("responseWriter should implement Unwrap()")
	}
}

func TestResponseWriter_ImplementsInterfaces(t *testing.T) {
	mock := newMockResponseWriter()
	rw := wrapResponseWriter(mock, nil, nil, nil)

	// Test that our wrapper always implements these interfaces
	if _, ok := rw.(http.Flusher); !ok {
		t.Error("responseWriter should implement http.Flusher")
	}

	if _, ok := rw.(http.Hijacker); !ok {
		t.Error("responseWriter should implement http.Hijacker")
	}

	if _, ok := rw.(ResponseWriter); !ok {
		t.Error("responseWriter should implement ResponseWriter")
	}
}

func TestResponseWriter_Flush_Supported(t *testing.T) {
	mock := &mockFlusherWriter{
		mockResponseWriter: newMockResponseWriter(),
	}
	rw := wrapResponseWriter(mock, nil, nil, nil)

	flusher, ok := rw.(http.Flusher)
	if !ok {
		t.Fatal("responseWriter should implement http.Flusher")
	}
	if _, ok := rw.(http.Pusher); !ok {
		t.Error("responseWriter should implement http.Pusher")
	}
	flusher.Flush()

	if !mock.flushCalled {
		t.Error("Flush() should delegate to underlying writer when supported")
	}
}

func TestResponseWriter_Flush_NotSupported(t *testing.T) {
	mock := newMockResponseWriter()
	rw := wrapResponseWriter(mock, nil, nil, nil)

	flusher, ok := rw.(http.Flusher)
	if !ok {
		t.Fatal("responseWriter should implement http.Flusher")
	}

	// Should not panic when underlying writer doesn't support Flush
	flusher.Flush() // Should be a no-op
}

func TestResponseWriter_Hijack_Supported(t *testing.T) {
	mock := &mockHijackerWriter{
		mockResponseWriter: newMockResponseWriter(),
	}
	rw := wrapResponseWriter(mock, nil, nil, nil)

	hijacker, ok := rw.(http.Hijacker)
	if !ok {
		t.Fatal("responseWriter should implement http.Hijacker")
	}

	_, _, err := hijacker.Hijack()
	if err == nil {
		t.Error("Expected error from mock hijacker")
	}

	if !mock.hijackCalled {
		t.Error("Hijack() should delegate to underlying writer when supported")
	}
}

func TestResponseWriter_Hijack_NotSupported(t *testing.T) {
	mock := newMockResponseWriter()
	rw := wrapResponseWriter(mock, nil, nil, nil)

	hijacker, ok := rw.(http.Hijacker)
	if !ok {
		t.Fatal("responseWriter should implement http.Hijacker")
	}

	_, _, err := hijacker.Hijack()
	if err == nil {
		t.Error("Hijack() should return error when underlying writer doesn't support it")
	}

	expectedErr := "feature not supported"
	if err.Error() != expectedErr {
		t.Errorf("Expected error message %q, got %q", expectedErr, err.Error())
	}
}

func TestResponseWriter_Push_Supported(t *testing.T) {
	mock := &mockPusherWriter{
		mockResponseWriter: newMockResponseWriter(),
	}
	rw := wrapResponseWriter(mock, nil, nil, nil)

	pusher, ok := rw.(http.Pusher)
	if !ok {
		t.Fatal("responseWriter should implement http.Pusher")
	}

	err := pusher.Push("/style.css", nil)
	if err != nil {
		t.Errorf("Push() failed: %v", err)
	}

	if !mock.pushCalled {
		t.Error("Push() should delegate to underlying writer when supported")
	}

	if mock.pushTarget != "/style.css" {
		t.Errorf("Expected push target /style.css, got %s", mock.pushTarget)
	}
}

func TestResponseWriter_Push_NotSupported(t *testing.T) {
	mock := newMockResponseWriter()
	rw := wrapResponseWriter(mock, nil, nil, nil)

	pusher, ok := rw.(http.Pusher)
	if !ok {
		t.Fatal("responseWriter should implement http.Pusher")
	}

	err := pusher.Push("/style.css", nil)
	if err == nil {
		t.Error("Push() should return error when underlying writer doesn't support it")
	}

	if err != http.ErrNotSupported {
		t.Errorf("Expected http.ErrNotSupported, got %v", err)
	}
}

func TestResponseWriter_AllInterfaces_Supported(t *testing.T) {
	mock := &mockFullWriter{
		mockResponseWriter: newMockResponseWriter(),
	}
	rw := wrapResponseWriter(mock, nil, nil, nil)

	// Test Flush
	flusher := rw.(http.Flusher)
	flusher.Flush()
	if !mock.flushCalled {
		t.Error("Flush() should work when all interfaces are supported")
	}

	// Test Hijack
	hijacker := rw.(http.Hijacker)
	_, _, err := hijacker.Hijack()
	if err == nil {
		t.Error("Expected error from mock hijacker")
	}
	if !mock.hijackCalled {
		t.Error("Hijack() should work when all interfaces are supported")
	}

	// Test Push
	pusher := rw.(http.Pusher)
	err = pusher.Push("/test", nil)
	if err != nil {
		t.Errorf("Push() failed: %v", err)
	}
	if !mock.pushCalled {
		t.Error("Push() should work when all interfaces are supported")
	}
}

func TestResponseWriter_WithHttpTestServer(t *testing.T) {
	// This tests integration with real httptest server
	mux := New()

	flusherWorks := false
	hijackerWorks := false
	pusherWorks := false

	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		// All interfaces should be available
		if _, ok := w.(http.Flusher); ok {
			flusherWorks = true
		}
		if _, ok := w.(http.Hijacker); ok {
			hijackerWorks = true
		}
		if _, ok := w.(http.Pusher); ok {
			pusherWorks = true
		}

		w.WriteHeader(http.StatusOK)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if !flusherWorks {
		t.Error("http.Flusher interface not available in handler")
	}

	if !hijackerWorks {
		t.Error("http.Hijacker interface not available in handler")
	}

	if !pusherWorks {
		t.Error("http.Pusher interface not available in handler")
	}

}
