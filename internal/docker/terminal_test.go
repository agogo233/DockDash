package docker

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dockdash/internal/config"
	"dockdash/internal/repo"
)

func TestTerminalOpenWriteReadAndClose(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/c1/exec", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Id":"exec1"}`))
	})
	mux.HandleFunc("/exec/exec1/start", func(w http.ResponseWriter, request *http.Request) {
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = io.CopyN(io.Discard, request.Body, request.ContentLength)
		request.Body.Close()
		_, _ = io.WriteString(conn, "HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
		go func() {
			defer conn.Close()
			data := make([]byte, 128)
			for {
				read, err := buffer.Read(data)
				if read > 0 {
					if _, err := conn.Write(data[:read]); err != nil {
						return
					}
				}
				if err != nil {
					return
				}
			}
		}()
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	manager := NewManager([]config.DockerHost{{Name: "local", Host: server.URL}})
	service := repo.Service{Source: "docker", Metadata: map[string]any{
		"dockerHostId": manager.hosts[0].ID, "containerId": "c1",
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := manager.OpenTerminal(ctx, "user-1", service, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if session.ID == "" || session.Owner != "user-1" {
		t.Fatalf("unexpected session: %+v", session)
	}
	if _, ok := manager.GetTerminal("other", session.ID); ok {
		t.Fatal("foreign owner can access terminal")
	}
	if err := session.Write("hello"); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 5)
	if _, err := io.ReadFull(session.Reader, data); err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("unexpected echo: %q", string(data))
	}
	manager.CloseTerminal(session.ID)
	if _, ok := manager.GetTerminal("user-1", session.ID); ok {
		t.Fatal("session was not closed")
	}
}
