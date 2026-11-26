package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dockdash/internal/config"
	"dockdash/internal/repo"
)

func buildArchive(t *testing.T, entries []struct {
	name string
	kind byte
	size int64
	data string
	mode int64
}) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, entry := range entries {
		header := &tar.Header{
			Name: entry.name, Typeflag: entry.kind, Size: entry.size,
			Mode: entry.mode, ModTime: testTime(),
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if entry.data != "" {
			if _, err := writer.Write([]byte(entry.data)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func testTime() (result time.Time) {
	return time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
}

func TestListAndReadFiles(t *testing.T) {
	archive := buildArchive(t, []struct {
		name string
		kind byte
		size int64
		data string
		mode int64
	}{
		{name: "sub/", kind: tar.TypeDir, mode: 0755},
		{name: "note.txt", kind: tar.TypeReg, size: 5, data: "hello", mode: 0644},
	})
	fileArchive := buildArchive(t, []struct {
		name string
		kind byte
		size int64
		data string
		mode int64
	}{
		{name: "note.txt", kind: tar.TypeReg, size: 5, data: "hello", mode: 0644},
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/c1/json", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"State":{"Running":true}}`))
	})
	mux.HandleFunc("/containers/c1/archive", func(w http.ResponseWriter, request *http.Request) {
		if strings.Contains(request.URL.Query().Get("path"), "note.txt") {
			_, _ = w.Write(fileArchive)
			return
		}
		_, _ = w.Write(archive)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	manager := NewManager([]config.DockerHost{{Name: "local", Host: server.URL}})
	service := repo.Service{Source: "docker", Metadata: map[string]any{
		"dockerHostId": manager.hosts[0].ID, "containerId": "c1",
	}}
	entries, err := manager.ListFiles(context.Background(), service, "/etc")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "sub" || entries[1].Name != "note.txt" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	content, err := manager.ReadFile(context.Background(), service, "/etc/note.txt", 1024)
	if err != nil || content.Content != "hello" {
		t.Fatalf("unexpected content: %+v %v", content, err)
	}
	_, err = manager.ReadFile(context.Background(), service, "/etc/note.txt", 2)
	var readError *FileReadError
	if !errors.As(err, &readError) || readError.Status != 413 {
		t.Fatalf("expected size error, got %v", err)
	}
}

func TestWriteFile(t *testing.T) {
	archive := buildArchive(t, []struct {
		name string
		kind byte
		size int64
		data string
		mode int64
	}{
		{name: "note.txt", kind: tar.TypeReg, size: 5, data: "hello", mode: 0644},
	})
	putCalled := false
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/c1/json", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"State":{"Running":true}}`))
	})
	mux.HandleFunc("/containers/c1/archive", func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			_, _ = w.Write(archive)
			return
		}
		putCalled = true
		reader := tar.NewReader(request.Body)
		header, err := reader.Next()
		if err != nil || header.Name != "note.txt" || header.Size != int64(len("world")) {
			t.Errorf("unexpected uploaded archive: %v %+v", err, header)
		}
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	manager := NewManager([]config.DockerHost{{Name: "local", Host: server.URL}})
	service := repo.Service{Source: "docker", Metadata: map[string]any{
		"dockerHostId": manager.hosts[0].ID, "containerId": "c1",
	}}
	if err := manager.WriteFile(context.Background(), service, "/etc/note.txt", "world"); err != nil {
		t.Fatal(err)
	}
	if !putCalled {
		t.Fatal("PUT archive was not called")
	}
}
