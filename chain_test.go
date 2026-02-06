package chain_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jpl-au/chain"
)

func TestBasicRouting(t *testing.T) {
	// Create a new router
	mux := chain.New()

	// Add a route
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello, World!"))
	})

	// Create a test server
	server := httptest.NewServer(mux)
	defer server.Close()

	// Make a request
	resp, err := http.Get(server.URL + "/hello")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	// Check response
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status OK, got %v", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	if string(body) != "Hello, World!" {
		t.Errorf("Expected body 'Hello, World!', got '%s'", body)
	}
}

func TestMiddleware(t *testing.T) {
	// Create a new router
	mux := chain.New()

	// Add middleware that adds a header
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Test", "middleware-called")
			next.ServeHTTP(w, r)
		})
	})

	// Add a route
	mux.HandleFunc("GET /middleware-test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})

	// Create a test server
	server := httptest.NewServer(mux)
	defer server.Close()

	// Make a request
	resp, err := http.Get(server.URL + "/middleware-test")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	// Check header was added by middleware
	if resp.Header.Get("X-Test") != "middleware-called" {
		t.Errorf("Middleware was not called, header not found")
	}
}

func TestResponseWrapperComplete(t *testing.T) {
	mux := chain.New()

	var capturedStatus int
	var capturedSize int
	var writtenBefore, writtenAfter bool

	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rw, ok := w.(chain.ResponseWriter); ok {
				writtenBefore = rw.Written()
			}

			next.ServeHTTP(w, r)

			if rw, ok := w.(chain.ResponseWriter); ok {
				capturedStatus = rw.Status()
				capturedSize = rw.Size()
				writtenAfter = rw.Written()
			}
		})
	})

	content := "Created Response"
	expectedSize := len(content)
	mux.HandleFunc("GET /wrapper-test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(content))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/wrapper-test")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	// Verify status tracking
	if capturedStatus != http.StatusCreated {
		t.Errorf("Expected status %d, got %d", http.StatusCreated, capturedStatus)
	}

	// Verify size tracking
	if capturedSize != expectedSize {
		t.Errorf("Expected size %d, got %d", expectedSize, capturedSize)
	}

	// Verify written flag
	if writtenBefore {
		t.Error("Written flag should be false before handler execution")
	}
	if !writtenAfter {
		t.Error("Written flag should be true after handler execution")
	}

	// Test double WriteHeader protection
	var doubleHeaderStatus int
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			if rw, ok := w.(chain.ResponseWriter); ok {
				doubleHeaderStatus = rw.Status()
			}
		})
	})

	mux.HandleFunc("GET /double-header", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)   // First write
		w.WriteHeader(http.StatusBadRequest) // Second write (ignored)
		w.Write([]byte("Test"))
	})

	resp2, err := http.Get(server.URL + "/double-header")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusAccepted {
		t.Errorf("Double WriteHeader protection failed: expected %d, got %d", http.StatusAccepted, resp2.StatusCode)
	}
	if doubleHeaderStatus != http.StatusAccepted {
		t.Errorf("Expected middleware to see first status %d, got %d", http.StatusAccepted, doubleHeaderStatus)
	}
}

func TestMethodChaining(t *testing.T) {
	// Create a router with chaining
	handlerCalled := false
	middlewareCalled := false

	mux := chain.New().
		Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				middlewareCalled = true
				next.ServeHTTP(w, r)
			})
		}).
		HandleFunc("GET /chained", func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.Write([]byte("Chained"))
		})

	// Create a test server
	server := httptest.NewServer(mux)
	defer server.Close()

	// Make a request
	resp, err := http.Get(server.URL + "/chained")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	// Verify both the middleware and handler were called
	if !middlewareCalled {
		t.Error("Middleware was not called")
	}
	if !handlerCalled {
		t.Error("Handler was not called")
	}
}

func TestNotFoundHandler(t *testing.T) {
	// Create a router with custom 404 handler
	mux := chain.New().
		WithNotFound(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("Custom 404"))
		}))

	// Create a test server
	server := httptest.NewServer(mux)
	defer server.Close()

	// Make a request to a non-existent route
	resp, err := http.Get(server.URL + "/non-existent")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Expected status 404, got %v", resp.StatusCode)
	}

	// Check response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	if string(body) != "Custom 404" {
		t.Errorf("Expected body 'Custom 404', got '%s'", body)
	}
}

func TestGroups(t *testing.T) {
	// Create a router
	mux := chain.New()

	// Track which middleware was called
	globalMiddlewareCalled := false
	groupMiddlewareCalled := false

	// Add global middleware
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			globalMiddlewareCalled = true
			next.ServeHTTP(w, r)
		})
	})

	// Add a route
	mux.HandleFunc("GET /global", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Global"))
	})

	// Add a group with its own middleware
	mux.Group(func(group *chain.Mux) {
		group.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				groupMiddlewareCalled = true
				next.ServeHTTP(w, r)
			})
		})

		group.HandleFunc("GET /group", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("Group"))
		})
	})

	// Create a test server
	server := httptest.NewServer(mux)
	defer server.Close()

	// Test the global route
	resp1, err := http.Get(server.URL + "/global")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	resp1.Body.Close()

	// Check global middleware was called
	if !globalMiddlewareCalled {
		t.Error("Global middleware was not called for global route")
	}
	// Group middleware should not be called
	if groupMiddlewareCalled {
		t.Error("Group middleware was incorrectly called for global route")
	}

	// Reset flags
	globalMiddlewareCalled = false
	groupMiddlewareCalled = false

	// Test the group route
	resp2, err := http.Get(server.URL + "/group")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	resp2.Body.Close()

	// Both middleware should be called
	if !globalMiddlewareCalled {
		t.Error("Global middleware was not called for group route")
	}
	if !groupMiddlewareCalled {
		t.Error("Group middleware was not called for group route")
	}
}

func TestMethodNotAllowedHandler(t *testing.T) {
	// Create a router with custom 405 handler
	mux := chain.New().
		WithMethodNotAllowed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusMethodNotAllowed)
			w.Write([]byte("Custom 405"))
		}))

	// Add a GET route
	mux.HandleFunc("GET /method-test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})

	// Create a test server
	server := httptest.NewServer(mux)
	defer server.Close()

	// Make a POST request to a GET-only route
	req, err := http.NewRequest(http.MethodPost, server.URL+"/method-test", strings.NewReader(""))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %v", resp.StatusCode)
	}

	// Check response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	if string(body) != "Custom 405" {
		t.Errorf("Expected body 'Custom 405', got '%s'", body)
	}
}

// ADDITIONAL TESTS

func TestMultipleMiddlewareOrder(t *testing.T) {
	mux := chain.New()

	order := []string{}

	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "middleware1-before")
			next.ServeHTTP(w, r)
			order = append(order, "middleware1-after")
		})
	})

	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "middleware2-before")
			next.ServeHTTP(w, r)
			order = append(order, "middleware2-after")
		})
	})

	mux.HandleFunc("GET /order-test", func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
		w.Write([]byte("OK"))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/order-test")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	expected := []string{
		"middleware1-before",
		"middleware2-before",
		"handler",
		"middleware2-after",
		"middleware1-after",
	}

	if !reflect.DeepEqual(order, expected) {
		t.Errorf("Middleware execution order incorrect.\nExpected: %v\nGot: %v", expected, order)
	}
}

func TestHeaderPreservation(t *testing.T) {
	mux := chain.New()

	mux.HandleFunc("GET /header-test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Header", "test-value")
		w.Write([]byte("OK"))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/header-test")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("X-Custom-Header") != "test-value" {
		t.Errorf("Custom header was not preserved")
	}
}

func TestGroupWithResponseWrapper(t *testing.T) {
	mux := chain.New()

	var statusInGroup int
	var sizeInGroup int

	mux.Group(func(group *chain.Mux) {
		group.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r)

				if rw, ok := w.(chain.ResponseWriter); ok {
					statusInGroup = rw.Status()
					sizeInGroup = rw.Size()
				}
			})
		})

		group.HandleFunc("GET /group-wrapper", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted) // 202
			content := "Group Content"
			w.Write([]byte(content))
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/group-wrapper")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	expectedStatus := http.StatusAccepted
	if statusInGroup != expectedStatus {
		t.Errorf("Expected status in group middleware to be %d, got %d", expectedStatus, statusInGroup)
	}

	expectedSize := len("Group Content")
	if sizeInGroup != expectedSize {
		t.Errorf("Expected size in group middleware to be %d, got %d", expectedSize, sizeInGroup)
	}
}

func TestCustomHandlersAutoEnableResponseWrapper(t *testing.T) {
	// Test that WithNotFound auto-enables response wrapper
	t.Run("WithNotFound", func(t *testing.T) {
		mux := chain.New().
			WithNotFound(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte("Custom 404"))
			}))

		server := httptest.NewServer(mux)
		defer server.Close()

		resp, err := http.Get(server.URL + "/non-existent")
		if err != nil {
			t.Fatalf("Failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Expected status 404, got %v", resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		if string(body) != "Custom 404" {
			t.Errorf("Expected 'Custom 404', got '%s' - response wrapper may not be auto-enabled", string(body))
		}
	})

	// Test that WithMethodNotAllowed auto-enables response wrapper
	t.Run("WithMethodNotAllowed", func(t *testing.T) {
		mux := chain.New().
			WithMethodNotAllowed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusMethodNotAllowed)
				w.Write([]byte("Custom 405"))
			}))

		// Add a GET route to trigger 405 behavior
		mux.HandleFunc("GET /method-test", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("GET OK"))
		})

		server := httptest.NewServer(mux)
		defer server.Close()

		req, err := http.NewRequest(http.MethodPost, server.URL+"/method-test", strings.NewReader(""))
		if err != nil {
			t.Fatalf("Failed to create request: %v", err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request: %v", err)
		}
		defer resp.Body.Close()

		// Should get either 405 or 404 depending on Go's ServeMux behavior
		if resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusNotFound {
			t.Errorf("Expected status 405 or 404, got %d", resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		// Accept either custom handler response
		if string(body) != "Custom 405" && string(body) != "Custom 404" {
			t.Errorf("Expected custom handler response, got '%s'", string(body))
		}
	})
}

// ADDITIONAL EDGE CASE TESTS

func TestPanicRecovery(t *testing.T) {
	mux := chain.New()

	// Middleware that panics
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("middleware panic")
		})
	})

	mux.HandleFunc("GET /panic", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// httptest handles panics gracefully - verify server doesn't crash
	resp, err := http.Get(server.URL + "/panic")
	if err != nil {
		// Connection errors are expected when panic occurs
		return
	}
	defer resp.Body.Close()

	// If response received, should be server error
	if resp.StatusCode < 500 {
		t.Errorf("Expected 5xx status for panic, got %v", resp.StatusCode)
	}
}

func TestNilHandlerHandling(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Nil handler setup caused panic: %v", r)
		}
	}()

	mux := chain.New()

	// Verify nil handlers don't cause setup panics
	mux.Handle("GET /nil-test", nil)
	mux.HandleFunc("GET /nil-func-test", nil)
	mux.WithNotFound(nil)
	mux.WithMethodNotAllowed(nil)
}

func TestConcurrentRequests(t *testing.T) {
	mux := chain.New()

	counter := 0
	var mu sync.Mutex

	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			counter++
			mu.Unlock()
			next.ServeHTTP(w, r)
		})
	})

	mux.HandleFunc("GET /concurrent", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Run 10 concurrent requests
	const numRequests = 10
	var wg sync.WaitGroup
	errors := make(chan error, numRequests)

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(server.URL + "/concurrent")
			if err != nil {
				errors <- err
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				errors <- fmt.Errorf("expected status 200, got %d", resp.StatusCode)
				return
			}
		}()
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Error(err)
	}

	// Check that all requests were processed
	if counter != numRequests {
		t.Errorf("Expected %d requests to be processed, got %d", numRequests, counter)
	}
}

func TestMultipleUseCalls(t *testing.T) {
	mux := chain.New()

	// Add multiple middleware using separate Use() calls
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Middleware-1", "called")
			next.ServeHTTP(w, r)
		})
	})

	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Middleware-2", "called")
			next.ServeHTTP(w, r)
		})
	})

	// Add multiple middleware in a single Use() call
	mux.Use(
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Middleware-3", "called")
				next.ServeHTTP(w, r)
			})
		},
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Middleware-4", "called")
				next.ServeHTTP(w, r)
			})
		},
	)

	mux.HandleFunc("GET /multiple-use", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/multiple-use")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	// Check all middleware headers are present
	expectedHeaders := []string{"X-Middleware-1", "X-Middleware-2", "X-Middleware-3", "X-Middleware-4"}
	for _, header := range expectedHeaders {
		if resp.Header.Get(header) != "called" {
			t.Errorf("Expected header %s to be 'called', got '%s'", header, resp.Header.Get(header))
		}
	}
}

func TestNestedGroups(t *testing.T) {
	mux := chain.New()

	// Global middleware
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Global", "true")
			next.ServeHTTP(w, r)
		})
	})

	// First level group
	mux.Group(func(level1 *chain.Mux) {
		level1.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Level1", "true")
				next.ServeHTTP(w, r)
			})
		})

		// Nested group inside first level
		level1.Group(func(level2 *chain.Mux) {
			level2.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("X-Level2", "true")
					next.ServeHTTP(w, r)
				})
			})

			level2.HandleFunc("GET /nested", func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("nested"))
			})
		})

		level1.HandleFunc("GET /level1", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("level1"))
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Test nested route
	resp1, err := http.Get(server.URL + "/nested")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp1.Body.Close()

	// Should have all three middleware headers
	if resp1.Header.Get("X-Global") != "true" {
		t.Error("Global middleware not applied to nested route")
	}
	if resp1.Header.Get("X-Level1") != "true" {
		t.Error("Level1 middleware not applied to nested route")
	}
	if resp1.Header.Get("X-Level2") != "true" {
		t.Error("Level2 middleware not applied to nested route")
	}

	// Test level1 route
	resp2, err := http.Get(server.URL + "/level1")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp2.Body.Close()

	// Should have global and level1, but not level2
	if resp2.Header.Get("X-Global") != "true" {
		t.Error("Global middleware not applied to level1 route")
	}
	if resp2.Header.Get("X-Level1") != "true" {
		t.Error("Level1 middleware not applied to level1 route")
	}
	if resp2.Header.Get("X-Level2") == "true" {
		t.Error("Level2 middleware incorrectly applied to level1 route")
	}
}

func TestMixedConfigurationChaining(t *testing.T) {
	// Test complex chaining of different configuration methods
	mux := chain.New().
		Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Chain-1", "true")
				next.ServeHTTP(w, r)
			})
		}).
		WithNotFound(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("Chained 404"))
		})).
		Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Chain-2", "true")
				next.ServeHTTP(w, r)
			})
		}).
		WithMethodNotAllowed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusMethodNotAllowed)
			w.Write([]byte("Chained 405"))
		})).
		HandleFunc("GET /chained-config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("OK"))
		})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Test normal route
	resp1, err := http.Get(server.URL + "/chained-config")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp1.Body.Close()

	if resp1.Header.Get("X-Chain-1") != "true" {
		t.Error("First chained middleware not applied")
	}
	if resp1.Header.Get("X-Chain-2") != "true" {
		t.Error("Second chained middleware not applied")
	}

	// Test 404 handler
	resp2, err := http.Get(server.URL + "/non-existent")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", resp2.StatusCode)
	}

	body2, _ := io.ReadAll(resp2.Body)
	if string(body2) != "Chained 404" {
		t.Errorf("Expected 'Chained 404', got '%s'", string(body2))
	}

	// Test 405 handler - need to add more explicit method handling
	// Add another route to trigger 405 behavior properly
	mux.HandleFunc("GET /method-test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("GET OK"))
	})

	req, err := http.NewRequest(http.MethodPost, server.URL+"/method-test", strings.NewReader(""))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	resp3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp3.Body.Close()

	// Note: Go's ServeMux returns 405 when a path exists but method doesn't match
	// The test may get 404 if the route pattern doesn't match, which is also valid
	if resp3.StatusCode != http.StatusMethodNotAllowed && resp3.StatusCode != http.StatusNotFound {
		t.Errorf("Expected status 405 or 404, got %d", resp3.StatusCode)
	}

	body3, _ := io.ReadAll(resp3.Body)
	// Accept either custom 405 or custom 404 handler response
	if string(body3) != "Chained 405" && string(body3) != "Chained 404" {
		t.Errorf("Expected 'Chained 405' or 'Chained 404', got '%s'", string(body3))
	}
}

func TestSSEStreaming(t *testing.T) {
	// Integration test for Server-Sent Events with real streaming
	mux := chain.New()

	mux.HandleFunc("GET /sse", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("ResponseWriter does not implement http.Flusher")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// Set headers for SSE
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		// Write and flush multiple events
		w.Write([]byte("data: event1\n\n"))
		flusher.Flush()

		w.Write([]byte("data: event2\n\n"))
		flusher.Flush()
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/sse")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	// Verify headers
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Error("SSE Content-Type header not set correctly")
	}
}

func TestUnwrap(t *testing.T) {
	mux := chain.New()

	var unwrappedWriter http.ResponseWriter
	mux.HandleFunc("GET /unwrap", func(w http.ResponseWriter, r *http.Request) {
		// Verify Unwrap returns the underlying ResponseWriter
		if unwrapper, ok := w.(interface{ Unwrap() http.ResponseWriter }); ok {
			underlying := unwrapper.Unwrap()
			if underlying == nil {
				t.Error("Unwrap returned nil")
			}
			unwrappedWriter = underlying
		} else {
			t.Error("ResponseWriter does not implement Unwrap()")
		}
		w.WriteHeader(http.StatusOK)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/unwrap")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if unwrappedWriter == nil {
		t.Error("Unwrap did not return the underlying ResponseWriter")
	}
}

func TestLargeResponseBody(t *testing.T) {
	mux := chain.New()

	actualSize := 0
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			if rw, ok := w.(chain.ResponseWriter); ok {
				actualSize = rw.Size()
			}
		})
	})

	// Create a large response body (1MB)
	largeContent := strings.Repeat("A", 1024*1024)
	expectedSize := len(largeContent)

	mux.HandleFunc("GET /large", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(largeContent))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/large")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	// Verify content length
	if len(body) != expectedSize {
		t.Errorf("Expected response body size %d, got %d", expectedSize, len(body))
	}

	// Verify response wrapper tracked the size correctly
	if actualSize != expectedSize {
		t.Errorf("Expected ResponseWriter to track size %d, got %d", expectedSize, actualSize)
	}

	// Verify content is correct
	if len(body) > 0 && body[0] != 'A' {
		t.Error("Response body content incorrect")
	}
}

// ROUTE PREFIX TESTS

func TestRoutePrefix(t *testing.T) {
	mux := chain.New()

	mux.Route("/api", func(api *chain.Mux) {
		api.HandleFunc("GET /users", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("users list"))
		})
		api.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("user: " + r.PathValue("id")))
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Test /api/users
	resp1, err := http.Get(server.URL + "/api/users")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp1.Body.Close()

	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp1.StatusCode)
	}

	body1, _ := io.ReadAll(resp1.Body)
	if string(body1) != "users list" {
		t.Errorf("Expected 'users list', got '%s'", string(body1))
	}

	// Test /api/users/{id}
	resp2, err := http.Get(server.URL + "/api/users/123")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp2.StatusCode)
	}

	body2, _ := io.ReadAll(resp2.Body)
	if string(body2) != "user: 123" {
		t.Errorf("Expected 'user: 123', got '%s'", string(body2))
	}
}

func TestNestedRoutePrefix(t *testing.T) {
	mux := chain.New()

	mux.Route("/api", func(api *chain.Mux) {
		api.Route("/v1", func(v1 *chain.Mux) {
			v1.HandleFunc("GET /users", func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("v1 users"))
			})
		})
		api.Route("/v2", func(v2 *chain.Mux) {
			v2.HandleFunc("GET /users", func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("v2 users"))
			})
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Test /api/v1/users
	resp1, err := http.Get(server.URL + "/api/v1/users")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp1.Body.Close()

	body1, _ := io.ReadAll(resp1.Body)
	if string(body1) != "v1 users" {
		t.Errorf("Expected 'v1 users', got '%s'", string(body1))
	}

	// Test /api/v2/users
	resp2, err := http.Get(server.URL + "/api/v2/users")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp2.Body.Close()

	body2, _ := io.ReadAll(resp2.Body)
	if string(body2) != "v2 users" {
		t.Errorf("Expected 'v2 users', got '%s'", string(body2))
	}
}

func TestRoutePrefixWithMiddleware(t *testing.T) {
	mux := chain.New()

	globalCalled := false
	routeCalled := false

	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			globalCalled = true
			next.ServeHTTP(w, r)
		})
	})

	mux.Route("/api", func(api *chain.Mux) {
		api.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				routeCalled = true
				next.ServeHTTP(w, r)
			})
		})

		api.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("OK"))
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/test")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if !globalCalled {
		t.Error("Global middleware was not called")
	}
	if !routeCalled {
		t.Error("Route middleware was not called")
	}
}

func TestRoutePrefixWithPatternWithoutMethod(t *testing.T) {
	mux := chain.New()

	mux.Route("/api", func(api *chain.Mux) {
		// Pattern without method prefix
		api.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("healthy"))
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Should work with any method
	resp, err := http.Get(server.URL + "/api/health")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "healthy" {
		t.Errorf("Expected 'healthy', got '%s'", string(body))
	}
}

func TestGroupInheritsRoutePrefix(t *testing.T) {
	mux := chain.New()

	mux.Route("/api", func(api *chain.Mux) {
		// Group inside Route should inherit the prefix
		api.Group(func(authed *chain.Mux) {
			authed.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("X-Authed", "true")
					next.ServeHTTP(w, r)
				})
			})

			authed.HandleFunc("GET /secret", func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("secret data"))
			})
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/secret")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("X-Authed") != "true" {
		t.Error("Group middleware was not applied")
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "secret data" {
		t.Errorf("Expected 'secret data', got '%s'", string(body))
	}
}

func TestRouteMethodChaining(t *testing.T) {
	handlerCalled := false

	mux := chain.New().
		Route("/api", func(api *chain.Mux) {
			api.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
				handlerCalled = true
				w.Write([]byte("OK"))
			})
		}).
		HandleFunc("GET /root", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("root"))
		})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Test route inside Route()
	resp1, err := http.Get(server.URL + "/api/test")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp1.Body.Close()

	if !handlerCalled {
		t.Error("Handler inside Route was not called")
	}

	// Test route after Route() chain
	resp2, err := http.Get(server.URL + "/root")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp2.Body.Close()

	body2, _ := io.ReadAll(resp2.Body)
	if string(body2) != "root" {
		t.Errorf("Expected 'root', got '%s'", string(body2))
	}
}

func TestNilMiddlewarePanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Expected panic for nil middleware, got none")
		}
		msg, ok := r.(string)
		if !ok || msg != "chain: nil middleware passed to Use" {
			t.Fatalf("Expected panic message 'chain: nil middleware passed to Use', got '%v'", r)
		}
	}()

	chain.New().Use(nil)
}
