package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dockdash/internal/repo"
)

type FileEntry struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	Permissions string `json:"permissions"`
	Modified    string `json:"modified"`
}

type FileContent struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type FileReadError struct {
	Message string
	Status  int
}

func (e *FileReadError) Error() string { return e.Message }

func (c *hostClient) raw(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	return c.streamClient.Do(request)
}

func (m *Manager) Logs(ctx context.Context, service repo.Service) (io.ReadCloser, bool, error) {
	host, id, err := m.serviceHost(service)
	if err != nil {
		return nil, false, err
	}
	info, err := m.inspectContainer(ctx, host, id)
	if err != nil {
		return nil, false, err
	}
	path := "/containers/" + url.PathEscape(id) + "/logs?follow=1&stdout=1&stderr=1&tail=100&timestamps=1"
	response, err := host.client.raw(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, false, err
	}
	if response.StatusCode >= http.StatusBadRequest {
		defer response.Body.Close()
		content, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return nil, false, fmt.Errorf("docker logs: %s", strings.TrimSpace(string(content)))
	}
	return response.Body, info.Config.Tty, nil
}

func (m *Manager) assertRunning(ctx context.Context, host Host, id string) error {
	var info struct {
		State struct {
			Running bool `json:"Running"`
		} `json:"State"`
	}
	if err := host.client.request(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil, &info); err != nil {
		return err
	}
	if !info.State.Running {
		return errors.New("Container is not running")
	}
	return nil
}

func archivePath(id, path string) string {
	return "/containers/" + url.PathEscape(id) + "/archive?path=" + url.QueryEscape(path)
}

func (m *Manager) archive(ctx context.Context, service repo.Service, path string) (io.ReadCloser, error) {
	host, id, err := m.serviceHost(service)
	if err != nil {
		return nil, err
	}
	if err := m.assertRunning(ctx, host, id); err != nil {
		return nil, err
	}
	response, err := host.client.raw(ctx, http.MethodGet, archivePath(id, path), nil, "")
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= http.StatusBadRequest {
		defer response.Body.Close()
		content, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return nil, fmt.Errorf("docker archive: %s", strings.TrimSpace(string(content)))
	}
	return response.Body, nil
}

func formatModified(value time.Time) string {
	if value.Year() != time.Now().Year() {
		return value.Format("Jan 2 2006")
	}
	return value.Format("Jan 2 15:04")
}

func (m *Manager) ListFiles(ctx context.Context, service repo.Service, path string) ([]FileEntry, error) {
	source, err := m.archive(ctx, service, path)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	reader := tar.NewReader(source)
	entries := []FileEntry{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(header.Name, "./"), "/"), "/")
		if name == "" || name == "." {
			continue
		}
		kind := "other"
		switch header.Typeflag {
		case tar.TypeDir:
			kind = "directory"
		case tar.TypeReg, tar.TypeRegA:
			kind = "file"
		case tar.TypeSymlink, tar.TypeLink:
			kind = "symlink"
		}
		entries = append(entries, FileEntry{
			Name: name, Type: kind, Size: header.Size,
			Permissions: header.FileInfo().Mode().String(), Modified: formatModified(header.ModTime),
		})
	}
	return entries, nil
}

func (m *Manager) ReadFile(ctx context.Context, service repo.Service, path string, maxBytes int64) (FileContent, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	source, err := m.archive(timeoutCtx, service, path)
	if err != nil {
		return FileContent{}, err
	}
	defer source.Close()
	reader := tar.NewReader(io.LimitReader(source, maxBytes+64*1024))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return FileContent{}, errors.New("File missing from archive")
		}
		if err != nil {
			return FileContent{}, err
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return FileContent{}, &FileReadError{Message:"Only regular files can be read", Status:400}
		}
		if header.Size > maxBytes {
			return FileContent{}, &FileReadError{Message:"File exceeds the read size limit", Status:413}
		}
		content, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
		if err != nil {
			return FileContent{}, err
		}
		if int64(len(content)) > maxBytes {
			return FileContent{}, &FileReadError{Message:"File exceeds the read size limit", Status:413}
		}
		return FileContent{Path: path, Content: string(content)}, nil
	}
}

func (m *Manager) WriteFile(ctx context.Context, service repo.Service, path string, content string) error {
	host, id, err := m.serviceHost(service)
	if err != nil {
		return err
	}
	if err := m.assertRunning(ctx, host, id); err != nil {
		return err
	}
	response, err := host.client.raw(ctx, http.MethodGet, archivePath(id, path), nil, "")
	if err != nil {
		return err
	}
	if response.StatusCode >= http.StatusBadRequest {
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return fmt.Errorf("docker archive: %s", strings.TrimSpace(string(body)))
	}
	source := response.Body
	var mode int64
	var uid, gid int
	var found bool
	reader := tar.NewReader(source)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			source.Close()
			return err
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			source.Close()
			return errors.New("Only regular files can be edited")
		}
		mode, uid, gid, found = header.Mode, header.Uid, header.Gid
		break
	}
	source.Close()
	if !found {
		return errors.New("File metadata missing from archive")
	}

	filename := path
	if index := strings.LastIndex(path, "/"); index >= 0 {
		filename = path[index+1:]
	}
	directory := "/"
	if index := strings.LastIndex(path, "/"); index > 0 {
		directory = path[:index]
	}
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	data := []byte(content)
	header := &tar.Header{
		Name: filename, Typeflag: tar.TypeReg, Mode: mode, Uid: uid, Gid: gid,
		Size: int64(len(data)), ModTime: time.Now(), Format: tar.FormatPAX,
	}
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	if _, err := writer.Write(data); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	putPath := "/containers/" + url.PathEscape(id) + "/archive?path=" + url.QueryEscape(directory) +
		"&copyUIDGID=1&noOverwriteDirNonDir=1"
	putResponse, err := host.client.raw(ctx, http.MethodPut, putPath, bytes.NewReader(buffer.Bytes()), "application/x-tar")
	if err != nil {
		return err
	}
	defer putResponse.Body.Close()
	if putResponse.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(putResponse.Body, 2048))
		return fmt.Errorf("docker archive: status %s: %s", strconv.Itoa(putResponse.StatusCode), strings.TrimSpace(string(body)))
	}
	_, _ = io.Copy(io.Discard, putResponse.Body)
	return nil
}

