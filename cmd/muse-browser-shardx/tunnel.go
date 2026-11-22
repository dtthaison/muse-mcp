package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type syncBuf struct {
	mu sync.Mutex
	b  []byte
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b = append(s.b, p...)
	return len(p), nil
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(string(s.b))
}

// tunnel is a running `ssh -N -L` forward to 127.0.0.1:<remote> on a host.
type tunnel struct {
	LocalPort int
	cmd       *exec.Cmd
	done      chan struct{}
	once      sync.Once
	stderr    *syncBuf
}

var tunnelReadyTimeout = 15 * time.Second

func validateSSHHost(h string) error {
	if h == "" {
		return errors.New("empty ssh host")
	}
	if strings.HasPrefix(h, "-") {
		return fmt.Errorf("invalid ssh host %q", h)
	}
	if strings.ContainsAny(h, " \t\r\n") {
		return fmt.Errorf("invalid ssh host %q (use a name from ~/.ssh/config for extra options)", h)
	}
	return nil
}

// sshArgs builds the ssh argument list for one local forward.
func sshArgs(host string, localPort, remotePort int) []string {
	return []string{
		"-N",
		"-o", "BatchMode=yes",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=30",
		"-L", fmt.Sprintf("%d:127.0.0.1:%d", localPort, remotePort),
		host,
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (t *tunnel) failure(host string, what string) error {
	msg := t.stderr.String()
	if msg == "" {
		return fmt.Errorf("ssh tunnel to %s: %s", host, what)
	}
	return fmt.Errorf("ssh tunnel to %s: %s: %s", host, what, msg)
}

// openTunnel starts ssh and waits until the local port accepts connections.
func openTunnel(ctx context.Context, host string, remotePort int) (*tunnel, error) {
	if err := validateSSHHost(host); err != nil {
		return nil, err
	}
	local, err := freePort()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("ssh", sshArgs(host, local, remotePort)...)
	t := &tunnel{LocalPort: local, cmd: cmd, done: make(chan struct{}), stderr: &syncBuf{}}
	cmd.Stderr = t.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ssh: %w", err)
	}
	go func() {
		_ = cmd.Wait()
		close(t.done)
	}()

	deadline := time.Now().Add(tunnelReadyTimeout)
	addr := fmt.Sprintf("127.0.0.1:%d", local)
	for {
		select {
		case <-ctx.Done():
			t.Close()
			return nil, ctx.Err()
		case <-t.done:
			return nil, t.failure(host, "ssh exited")
		default:
		}
		if c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
			c.Close()
			select {
			case <-t.done:
				return nil, t.failure(host, "ssh exited")
			default:
			}
			return t, nil
		}
		if time.Now().After(deadline) {
			err := t.failure(host, fmt.Sprintf("local port %d not ready after %s", local, tunnelReadyTimeout))
			t.Close()
			return nil, err
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
		case <-t.done:
		}
	}
}

// Close kills ssh. Safe to call more than once and on nil.
func (t *tunnel) Close() {
	if t == nil {
		return
	}
	t.once.Do(func() {
		if t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
		select {
		case <-t.done:
		case <-time.After(3 * time.Second):
		}
	})
}
