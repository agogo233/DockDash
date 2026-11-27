package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"dockdash/internal/repo"
)

const terminalIdleTTL = 10 * time.Minute

type TerminalSession struct {
	ID           string
	Owner        string
	Conn         net.Conn
	Reader       *bufio.Reader
	LastActivity time.Time
}

type TerminalManager struct {
	mu       sync.Mutex
	sessions map[string]*TerminalSession
}

func NewTerminalManager() *TerminalManager {
	return &TerminalManager{sessions: map[string]*TerminalSession{}}
}

func (m *TerminalManager) add(session *TerminalSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.ID] = session
}

func (m *TerminalManager) Get(owner, id string) (*TerminalSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok || session.Owner != owner {
		return nil, false
	}
	return session, true
}

func (m *TerminalManager) Touch(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if session, ok := m.sessions[id]; ok {
		session.LastActivity = time.Now()
	}
}

func (m *TerminalManager) Close(id string) {
	m.mu.Lock()
	session, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if ok {
		_ = session.Conn.Close()
	}
}

func (m *TerminalManager) Sweep(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.mu.Lock()
				expired := []string{}
				for id, session := range m.sessions {
					if time.Since(session.LastActivity) > terminalIdleTTL {
						expired = append(expired, id)
					}
				}
				m.mu.Unlock()
				for _, id := range expired {
					m.Close(id)
				}
			}
		}
	}()
}

func (m *Manager) Start(ctx context.Context) {
	m.terminals.Sweep(ctx)
}

func (m *Manager) Shutdown() {
	m.terminals.Shutdown()
}

func (m *TerminalManager) Shutdown() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Close(id)
	}
}

func (m *Manager) CloseTerminal(id string) {
	m.terminals.Close(id)
}

func (m *Manager) GetTerminal(owner, id string) (*TerminalSession, bool) {
	return m.terminals.Get(owner, id)
}

func (m *Manager) TouchTerminal(id string) {
	m.terminals.Touch(id)
}

func (c *hostClient) hijack(ctx context.Context, path, body string) (net.Conn, *bufio.Reader, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	request := "POST " + path + " HTTP/1.1\r\n" +
		"Host: " + c.hostHeader + "\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: tcp\r\n" +
		"Content-Type: application/json\r\n" +
		"Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body
	if _, err := io.WriteString(conn, request); err != nil {
		conn.Close()
		return nil, nil, err
	}
	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	if !strings.Contains(statusLine, " 101 ") {
		message := strings.TrimSpace(statusLine)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			if line == "\r\n" {
				break
			}
		}
		remaining, _ := io.ReadAll(io.LimitReader(reader, 2048))
		conn.Close()
		return nil, nil, errors.New(message + ": " + strings.TrimSpace(string(remaining)))
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			conn.Close()
			return nil, nil, err
		}
		if line == "\r\n" {
			break
		}
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, reader, nil
}

func (m *Manager) OpenTerminal(ctx context.Context, owner string, service repo.Service, cols, rows int) (*TerminalSession, error) {
	host, id, err := m.serviceHost(service)
	if err != nil {
		return nil, err
	}
	config := map[string]any{
		"AttachStdin":  true,
		"AttachStdout": true,
		"AttachStderr": true,
		"Tty":          true,
		"Cmd": []string{
			"/bin/sh", "-c",
			"TERM=xterm-256color; export TERM; [ -x /bin/bash ] && exec bash || exec sh",
		},
		"Env": []string{"COLUMNS=" + strconv.Itoa(cols), "LINES=" + strconv.Itoa(rows)},
	}
	payload, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := host.client.request(ctx, "POST", "/containers/"+url.PathEscape(id)+"/exec", bytes.NewReader(payload), &created); err != nil {
		return nil, err
	}
	if created.ID == "" {
		return nil, errors.New("Docker exec did not return an ID")
	}
	conn, reader, err := host.client.hijack(ctx, "/exec/"+created.ID+"/start", `{"Detach":false,"Tty":true}`)
	if err != nil {
		return nil, err
	}
	session := &TerminalSession{
		ID: repo.NewID(), Owner: owner, Conn: conn, Reader: reader, LastActivity: time.Now(),
	}
	m.terminals.add(session)
	return session, nil
}

func (s *TerminalSession) Write(data string) error {
	_ = s.Conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	defer s.Conn.SetWriteDeadline(time.Time{})
	_, err := io.WriteString(s.Conn, data)
	return err
}
