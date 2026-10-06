package chain

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"testing"
)

// Tests are organised by the method under test (TestResponseWriter_Method)
// with one table-driven subtest per behaviour. They exercise the wrapper
// directly against mock writers; end-to-end behaviour through a real server
// is covered in chain_test.go.

// mockWriter is a bare http.ResponseWriter with no optional interfaces.
type mockWriter struct {
	header http.Header
	status int
	body   []byte
}

func newMockWriter() *mockWriter { return &mockWriter{header: http.Header{}} }

func (m *mockWriter) Header() http.Header { return m.header }

func (m *mockWriter) Write(b []byte) (int, error) {
	m.body = append(m.body, b...)
	return len(b), nil
}

func (m *mockWriter) WriteHeader(status int) { m.status = status }

// mockFlusher adds http.Flusher.
type mockFlusher struct {
	*mockWriter
	flushed bool
}

func (m *mockFlusher) Flush() { m.flushed = true }

// mockHijacker adds http.Hijacker, returning err from Hijack.
type mockHijacker struct {
	*mockWriter
	err      error
	hijacked bool
}

func (m *mockHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	m.hijacked = true
	return nil, nil, m.err
}

// mockPusher adds http.Pusher.
type mockPusher struct {
	*mockWriter
	target string
}

func (m *mockPusher) Push(target string, _ *http.PushOptions) error {
	m.target = target
	return nil
}

// mockFull adds all three optional interfaces.
type mockFull struct {
	*mockFlusher
	*mockHijacker
	*mockPusher
}

func newMockFull() *mockFull {
	w := newMockWriter()
	return &mockFull{&mockFlusher{mockWriter: w}, &mockHijacker{mockWriter: w, err: errors.New("mock hijack")}, &mockPusher{mockWriter: w}}
}

func (m *mockFull) Header() http.Header         { return m.mockFlusher.Header() }
func (m *mockFull) Write(b []byte) (int, error) { return m.mockFlusher.Write(b) }
func (m *mockFull) WriteHeader(status int)      { m.mockFlusher.WriteHeader(status) }

func TestResponseWriter_Status(t *testing.T) {
	tests := []struct {
		name string
		act  func(*responseWriter)
		want int
	}{
		{"defaults to 200", func(*responseWriter) {}, http.StatusOK},
		{"after WriteHeader", func(rw *responseWriter) { rw.WriteHeader(http.StatusCreated) }, http.StatusCreated},
		{"after Write only", func(rw *responseWriter) { rw.Write([]byte("x")) }, http.StatusOK},
		{"first WriteHeader wins", func(rw *responseWriter) {
			rw.WriteHeader(http.StatusAccepted)
			rw.WriteHeader(http.StatusBadRequest)
		}, http.StatusAccepted},
		{"ignores informational status", func(rw *responseWriter) { rw.WriteHeader(http.StatusEarlyHints) }, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rw := wrapResponseWriter(newMockWriter())
			tt.act(rw)
			if got := rw.Status(); got != tt.want {
				t.Errorf("Status() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestResponseWriter_Size(t *testing.T) {
	tests := []struct {
		name string
		act  func(*responseWriter)
		want int
	}{
		{"nothing written", func(*responseWriter) {}, 0},
		{"single write", func(rw *responseWriter) { rw.Write([]byte("hello")) }, 5},
		{"accumulates", func(rw *responseWriter) {
			rw.Write([]byte("hello"))
			rw.Write([]byte(", world"))
		}, 12},
		{"header only", func(rw *responseWriter) { rw.WriteHeader(http.StatusNoContent) }, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rw := wrapResponseWriter(newMockWriter())
			tt.act(rw)
			if got := rw.Size(); got != tt.want {
				t.Errorf("Size() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestResponseWriter_Written(t *testing.T) {
	tests := []struct {
		name string
		act  func(*responseWriter)
		want bool
	}{
		{"nothing written", func(*responseWriter) {}, false},
		{"after WriteHeader", func(rw *responseWriter) { rw.WriteHeader(http.StatusOK) }, true},
		{"after Write", func(rw *responseWriter) { rw.Write([]byte("x")) }, true},
		{"after informational status", func(rw *responseWriter) { rw.WriteHeader(http.StatusEarlyHints) }, false},
		{"after 101 Switching Protocols", func(rw *responseWriter) { rw.WriteHeader(http.StatusSwitchingProtocols) }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rw := wrapResponseWriter(newMockWriter())
			tt.act(rw)
			if got := rw.Written(); got != tt.want {
				t.Errorf("Written() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResponseWriter_WriteHeader(t *testing.T) {
	tests := []struct {
		name           string
		capturing      bool
		statuses       []int
		wantUnderlying int // status received by the underlying writer
		wantStatus     int // Status() reported by the wrapper
		wantWritten    bool
		wantCaptured   int
	}{
		{
			name:           "passes the status through",
			statuses:       []int{http.StatusCreated},
			wantUnderlying: http.StatusCreated,
			wantStatus:     http.StatusCreated,
			wantWritten:    true,
		},
		{
			name:           "ignores a second call",
			statuses:       []int{http.StatusAccepted, http.StatusBadRequest},
			wantUnderlying: http.StatusAccepted,
			wantStatus:     http.StatusAccepted,
			wantWritten:    true,
		},
		{
			name:           "1xx passes through without finalising",
			statuses:       []int{http.StatusEarlyHints},
			wantUnderlying: http.StatusEarlyHints,
			wantStatus:     http.StatusOK,
			wantWritten:    false,
		},
		{
			name:           "final status after 1xx is honoured",
			statuses:       []int{http.StatusEarlyHints, http.StatusNotFound},
			wantUnderlying: http.StatusNotFound,
			wantStatus:     http.StatusNotFound,
			wantWritten:    true,
		},
		{
			name:           "101 finalises like net/http",
			statuses:       []int{http.StatusSwitchingProtocols, http.StatusOK},
			wantUnderlying: http.StatusSwitchingProtocols,
			wantStatus:     http.StatusSwitchingProtocols,
			wantWritten:    true,
		},
		{
			name:         "captures instead of writing while capturing",
			capturing:    true,
			statuses:     []int{http.StatusNotFound},
			wantCaptured: http.StatusNotFound,
			wantStatus:   http.StatusOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newMockWriter()
			rw := wrapResponseWriter(mock)
			rw.capturing = tt.capturing
			for _, s := range tt.statuses {
				rw.WriteHeader(s)
			}
			if mock.status != tt.wantUnderlying {
				t.Errorf("underlying status = %d, want %d", mock.status, tt.wantUnderlying)
			}
			if rw.Status() != tt.wantStatus || rw.Written() != tt.wantWritten {
				t.Errorf("Status()=%d Written()=%v, want %d %v", rw.Status(), rw.Written(), tt.wantStatus, tt.wantWritten)
			}
			if rw.captured != tt.wantCaptured {
				t.Errorf("captured = %d, want %d", rw.captured, tt.wantCaptured)
			}
		})
	}
}

func TestResponseWriter_Write(t *testing.T) {
	tests := []struct {
		name           string
		capturing      bool
		writes         []string
		wantUnderlying string
		wantCaptured   string
		wantSize       int
		wantWritten    bool
	}{
		{
			name:           "forwards bytes and counts them",
			writes:         []string{"hello", " world"},
			wantUnderlying: "hello world",
			wantSize:       11,
			wantWritten:    true,
		},
		{
			name:         "buffers instead of writing while capturing",
			capturing:    true,
			writes:       []string{"404 page", " not found\n"},
			wantCaptured: "404 page not found\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newMockWriter()
			rw := wrapResponseWriter(mock)
			if tt.capturing {
				rw.capturing = true
				rw.WriteHeader(http.StatusNotFound)
			}
			for _, w := range tt.writes {
				n, err := rw.Write([]byte(w))
				if err != nil || n != len(w) {
					t.Errorf("Write(%q) = %d, %v; want %d, nil", w, n, err, len(w))
				}
			}
			if string(mock.body) != tt.wantUnderlying {
				t.Errorf("underlying body = %q, want %q", mock.body, tt.wantUnderlying)
			}
			if string(rw.capturedBody) != tt.wantCaptured {
				t.Errorf("captured body = %q, want %q", rw.capturedBody, tt.wantCaptured)
			}
			if rw.Size() != tt.wantSize || rw.Written() != tt.wantWritten {
				t.Errorf("Size()=%d Written()=%v, want %d %v", rw.Size(), rw.Written(), tt.wantSize, tt.wantWritten)
			}
		})
	}
}

func TestResponseWriter_Unwrap(t *testing.T) {
	t.Run("returns the underlying writer", func(t *testing.T) {
		mock := newMockWriter()
		rw := wrapResponseWriter(mock)
		if got := rw.Unwrap(); got != http.ResponseWriter(mock) {
			t.Errorf("Unwrap() = %v, want the wrapped writer", got)
		}
	})
}

func TestResponseWriter_Flush(t *testing.T) {
	tests := []struct {
		name        string
		underlying  func() (http.ResponseWriter, *bool)
		wantWritten bool
	}{
		{
			name: "delegates when supported",
			underlying: func() (http.ResponseWriter, *bool) {
				m := &mockFlusher{mockWriter: newMockWriter()}
				return m, &m.flushed
			},
			wantWritten: true,
		},
		{
			name: "no-op when unsupported",
			underlying: func() (http.ResponseWriter, *bool) {
				return newMockWriter(), new(bool)
			},
			wantWritten: false,
		},
		{
			name: "delegates when all interfaces are present",
			underlying: func() (http.ResponseWriter, *bool) {
				m := newMockFull()
				return m, &m.flushed
			},
			wantWritten: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, flushed := tt.underlying()
			rw := wrapResponseWriter(w)
			rw.Flush()
			if *flushed != tt.wantWritten {
				t.Errorf("underlying flushed = %v, want %v", *flushed, tt.wantWritten)
			}
			if rw.Written() != tt.wantWritten {
				t.Errorf("Written() = %v, want %v", rw.Written(), tt.wantWritten)
			}
			if tt.wantWritten && rw.Status() != http.StatusOK {
				t.Errorf("Status() = %d after flush, want 200", rw.Status())
			}
		})
	}
}

func TestResponseWriter_Hijack(t *testing.T) {
	tests := []struct {
		name        string
		underlying  func() (http.ResponseWriter, *bool)
		wantErr     error
		wantWritten bool
	}{
		{
			name: "delegates and returns the underlying error",
			underlying: func() (http.ResponseWriter, *bool) {
				m := &mockHijacker{mockWriter: newMockWriter(), err: errors.New("mock hijack")}
				return m, &m.hijacked
			},
			wantErr: errors.New("mock hijack"),
		},
		{
			name: "marks the response written on success",
			underlying: func() (http.ResponseWriter, *bool) {
				m := &mockHijacker{mockWriter: newMockWriter()}
				return m, &m.hijacked
			},
			wantWritten: true,
		},
		{
			name: "returns ErrNotSupported when unsupported",
			underlying: func() (http.ResponseWriter, *bool) {
				return newMockWriter(), new(bool)
			},
			wantErr: http.ErrNotSupported,
		},
		{
			name: "delegates when all interfaces are present",
			underlying: func() (http.ResponseWriter, *bool) {
				m := newMockFull()
				return m, &m.hijacked
			},
			wantErr: errors.New("mock hijack"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, hijacked := tt.underlying()
			rw := wrapResponseWriter(w)
			_, _, err := rw.Hijack()
			switch {
			case tt.wantErr == nil && err != nil:
				t.Errorf("Hijack() error = %v, want nil", err)
			case tt.wantErr != nil && (err == nil || err.Error() != tt.wantErr.Error()):
				t.Errorf("Hijack() error = %v, want %v", err, tt.wantErr)
			}
			if _, ok := w.(http.Hijacker); ok && !*hijacked {
				t.Error("Hijack() did not delegate to the underlying writer")
			}
			if rw.Written() != tt.wantWritten {
				t.Errorf("Written() = %v, want %v", rw.Written(), tt.wantWritten)
			}
		})
	}
}

func TestResponseWriter_Push(t *testing.T) {
	tests := []struct {
		name       string
		underlying func() (http.ResponseWriter, *string)
		wantErr    error
	}{
		{
			name: "delegates when supported",
			underlying: func() (http.ResponseWriter, *string) {
				m := &mockPusher{mockWriter: newMockWriter()}
				return m, &m.target
			},
		},
		{
			name: "returns ErrNotSupported when unsupported",
			underlying: func() (http.ResponseWriter, *string) {
				return newMockWriter(), new(string)
			},
			wantErr: http.ErrNotSupported,
		},
		{
			name: "delegates when all interfaces are present",
			underlying: func() (http.ResponseWriter, *string) {
				m := newMockFull()
				return m, &m.target
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, target := tt.underlying()
			rw := wrapResponseWriter(w)
			err := rw.Push("/style.css", nil)
			if err != tt.wantErr {
				t.Errorf("Push() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && *target != "/style.css" {
				t.Errorf("underlying push target = %q, want /style.css", *target)
			}
		})
	}
}

func TestResponseWriter_Interfaces(t *testing.T) {
	t.Run("always satisfies the optional interfaces", func(t *testing.T) {
		// The wrapper advertises every interface regardless of the underlying
		// writer, so handlers can type-assert once and rely on delegation.
		var w http.ResponseWriter = wrapResponseWriter(newMockWriter())
		checks := map[string]bool{
			"http.Flusher":         func() bool { _, ok := w.(http.Flusher); return ok }(),
			"http.Hijacker":        func() bool { _, ok := w.(http.Hijacker); return ok }(),
			"http.Pusher":          func() bool { _, ok := w.(http.Pusher); return ok }(),
			"chain.ResponseWriter": func() bool { _, ok := w.(ResponseWriter); return ok }(),
		}
		for name, ok := range checks {
			if !ok {
				t.Errorf("responseWriter does not implement %s", name)
			}
		}
	})
}
