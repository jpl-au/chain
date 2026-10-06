package chain

import (
	"net/http"
	"testing"
)

// FuzzResponseWriter drives random call sequences at the wrapper and checks
// its invariants against a simple model of what the underlying writer should
// have received. Each byte of ops encodes one call:
//
//	0: WriteHeader(final status)   1: WriteHeader(1xx)   2: Write(body)
//	3: Flush                       4: stop capturing (a route was entered,
//	                                  or a captured response is being replayed)
//
// Invariants checked: the underlying writer receives at most one final
// status; nothing reaches it while capturing; the first final status wins
// in both modes; Size() counts only forwarded bytes; Written() is true
// exactly when something reached the underlying writer.
func FuzzResponseWriter(f *testing.F) {
	f.Add(false, []byte{0, 2})
	f.Add(false, []byte{2})
	f.Add(false, []byte{1, 0, 2})
	f.Add(false, []byte{0, 0, 2})
	f.Add(false, []byte{3, 0})
	f.Add(true, []byte{0, 2, 2})
	f.Add(true, []byte{4, 0, 2})
	f.Add(true, []byte{2})
	f.Add(true, []byte{0, 0})
	f.Add(true, []byte{1, 0})

	f.Fuzz(func(t *testing.T, capturing bool, ops []byte) {
		mock := &mockFlusher{mockWriter: newMockWriter()}
		rw := wrapResponseWriter(mock)
		rw.capturing = capturing

		// model
		var (
			finalHeaders int  // non-1xx WriteHeader calls that reached the underlying writer
			forwarded    int  // bytes that reached the underlying writer
			firstStatus  int  // first final status the wrapper accepted (captured or written)
			captured     bool // a final status was captured while capturing
		)
		for i, op := range ops {
			status := http.StatusNotFound
			if i%2 == 1 {
				status = http.StatusTeapot
			}
			wasCapturing := rw.capturing
			beforeStatus, beforeBody, beforeFlushed := mock.status, len(mock.body), mock.flushed
			switch op % 5 {
			case 0:
				rw.WriteHeader(status)
				if firstStatus == 0 {
					firstStatus = status
					captured = wasCapturing
				}
			case 1:
				rw.WriteHeader(http.StatusEarlyHints)
			case 2:
				rw.Write([]byte("abc"))
				if firstStatus == 0 {
					// An implicit 200, exactly as net/http would send.
					firstStatus = http.StatusOK
					captured = wasCapturing
				}
			case 3:
				rw.Flush()
				// A flush sends an implicit 200 when nothing has been written yet.
				// While capturing there is nothing to flush, so it commits nothing.
				if firstStatus == 0 && !wasCapturing {
					firstStatus = http.StatusOK
				}
			case 4:
				// Mux.ServeHTTP hands a captured response to serveUnrouted, which
				// clears the capture before the replay enters a route and stops
				// capturing. Model that transition: the replay is a fresh response.
				// Capturing only ever stops once, so this is a no-op afterwards.
				if !rw.capturing {
					break
				}
				rw.captured, rw.capturedBody = 0, nil
				rw.capturing = false
				firstStatus, captured = 0, false
			}
			if mock.status != beforeStatus && mock.status >= 200 {
				finalHeaders++
			}
			forwarded += len(mock.body) - beforeBody
			if wasCapturing && (mock.status != beforeStatus || len(mock.body) != beforeBody || mock.flushed != beforeFlushed) {
				t.Fatalf("op %d (%d) reached the underlying writer while capturing", i, op%5)
			}
		}

		if finalHeaders > 1 {
			t.Fatalf("underlying writer received %d final WriteHeader calls", finalHeaders)
		}
		if rw.Size() != forwarded {
			t.Fatalf("Size() = %d, but %d bytes reached the underlying writer", rw.Size(), forwarded)
		}
		if captured {
			if rw.captured != firstStatus {
				t.Fatalf("captured = %d, want first status %d", rw.captured, firstStatus)
			}
			if forwarded != 0 || finalHeaders != 0 || mock.flushed {
				t.Fatalf("captured response leaked to the underlying writer: bytes=%d headers=%d flushed=%v", forwarded, finalHeaders, mock.flushed)
			}
			if rw.Written() {
				t.Fatal("Written() is true for a captured response")
			}
			return
		}
		if firstStatus != 0 && rw.Status() != firstStatus {
			t.Fatalf("Status() = %d, want first status %d", rw.Status(), firstStatus)
		}
		if firstStatus == 0 && rw.Status() != http.StatusOK {
			t.Fatalf("Status() = %d with no status written, want 200", rw.Status())
		}
		touched := finalHeaders > 0 || forwarded > 0 || mock.flushed
		if rw.Written() != touched {
			t.Fatalf("Written() = %v, but underlying touched = %v", rw.Written(), touched)
		}
	})
}
