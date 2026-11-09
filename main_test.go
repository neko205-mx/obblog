package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func availableAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	l.Close()
	return address
}

func waitForTestServer(t *testing.T, base string, result <-chan error) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-result:
			t.Fatalf("server stopped: %v", err)
		default:
		}
		response, err := client.Get(base + "/install/api/state")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("test server failed to start")
}

func TestTestModeShutdownRemovesEverything(t *testing.T) {
	for _, stop := range []string{"cancel", "web"} {
		t.Run(stop, func(t *testing.T) {
			parent := t.TempDir()
			t.Setenv("TMPDIR", parent)
			persistent := t.TempDir()
			marker := filepath.Join(persistent, "config.json")
			if err := os.WriteFile(marker, []byte("persistent installation must not be loaded"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			address := availableAddress(t)
			result := make(chan error, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				result <- run(ctx, startOptions{Test: true, Data: persistent, Addr: address})
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("server did not stop during test cleanup")
				}
			})
			base := "http://" + address
			waitForTestServer(t, base, result)
			jar, _ := cookiejar.New(nil)
			client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
			request := func(endpoint string, body any) []byte {
				t.Helper()
				raw, _ := json.Marshal(body)
				response, err := client.Post(base+endpoint, "application/json", bytes.NewReader(raw))
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				data, _ := io.ReadAll(response.Body)
				if response.StatusCode >= 300 {
					t.Fatalf("%s: %d %s", endpoint, response.StatusCode, data)
				}
				return data
			}
			request("/install/api/configure", map[string]any{"mode": "empty", "password": "temporary-test-password", "confirmation": "temporary-test-password"})
			request("/admin/api/login", map[string]string{"password": "temporary-test-password"})
			request("/admin/api/note", map[string]string{"path": "test.md", "source": "# Disposable note\n"})
			entries, _ := os.ReadDir(parent)
			if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "obblog-test-") {
				t.Fatal("test installation did not use one dedicated sandbox")
			}
			if stop == "web" {
				request("/admin/api/test/stop", nil)
			} else {
				cancel()
			}
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("test server did not shut down")
			}
			entries, _ = os.ReadDir(parent)
			if len(entries) != 0 {
				t.Fatal("temporary files remained after server shutdown")
			}
			raw, err := os.ReadFile(marker)
			if err != nil || string(raw) != "persistent installation must not be loaded" {
				t.Fatal("test mode changed persistent data")
			}
		})
	}
}

func TestTestModeBindFailureStillCleansUp(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("TMPDIR", parent)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := run(context.Background(), startOptions{Test: true, Addr: listener.Addr().String()}); err == nil {
		t.Fatal("occupied port accepted")
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 0 {
		t.Fatal("startup failure leaked test environment")
	}
}
