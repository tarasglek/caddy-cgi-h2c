// Copyright 2015 Matthew Holt and The Caddy Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cgih2c

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/net/http2"

	"github.com/caddyserver/caddy/v2"
)

const startupFailureCooldown = 250 * time.Millisecond

func init() {
	caddy.RegisterModule(Transport{})
}

// Transport starts the configured command on first use and proxies requests over
// a shared h2c session on the process's stdin/stdout. When restart is enabled,
// a RoundTrip error discards the shared session, which may also affect other
// in-flight streams on that session.
type Transport struct {
	// Command is the executable path used to start the backend process.
	Command string `json:"command,omitempty"`

	// Args are passed to Command when starting the backend process.
	Args []string `json:"args,omitempty"`

	// Dir is the working directory for the backend process.
	Dir string `json:"dir,omitempty"`

	// EnvVars are extra environment variables for the backend process. Values may use global placeholders.
	EnvVars map[string]string `json:"env,omitempty"`

	// Restart controls whether the backend session is discarded and recreated after a RoundTrip error. Defaults to true.
	// Cleanup always stops the current backend process, and a later request may start a new one.
	Restart *bool `json:"restart,omitempty"`

	// CaptureStderr controls whether stderr from the backend process is captured and logged.
	CaptureStderr bool `json:"capture_stderr,omitempty"`

	// ShutdownTimeout is how long to wait for the backend process to stop before killing it. Default: 2s.
	ShutdownTimeout caddy.Duration `json:"shutdown_timeout,omitempty"`

	logger *zap.Logger

	mu    sync.Mutex
	cond  *sync.Cond
	child *childProcess
	conn  net.Conn
	cc    *http2.ClientConn

	stopping             bool
	lastStartFailure     error
	lastStartFailureTime time.Time
}

// CaddyModule returns the Caddy module information.
func (Transport) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.reverse_proxy.transport.cgi_h2c",
		New: func() caddy.Module { return new(Transport) },
	}
}

// Provision sets up t for proxying requests to the configured child process.
func (t *Transport) Provision(ctx caddy.Context) error {
	if t.Command == "" {
		return errors.New("command is required")
	}
	t.logger = ctx.Logger()
	if t.ShutdownTimeout == 0 {
		t.ShutdownTimeout = caddy.Duration(2 * time.Second)
	}
	if t.ShutdownTimeout < 0 {
		return errors.New("shutdown_timeout cannot be negative")
	}
	for key := range t.EnvVars {
		if key == "" {
			return errors.New("env key cannot be empty")
		}
		if strings.Contains(key, "=") {
			return fmt.Errorf("env key %q cannot contain '='", key)
		}
	}
	if t.cond == nil {
		t.cond = sync.NewCond(&t.mu)
	}
	return nil
}

// RoundTrip proxies req to the child process over the shared h2c session.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// HTTP/2 client requests require a scheme and authority. The reverse proxy
	// normally sets them, but fill harmless cgi-h2c-local defaults for direct use
	// in tests or custom callers.
	if req.URL.Scheme == "" || req.URL.Host == "" {
		req2 := req.Clone(req.Context())
		u := *req.URL
		if u.Scheme == "" {
			u.Scheme = "http"
		}
		if u.Host == "" {
			u.Host = "cgi-h2c.local"
		}
		req2.URL = &u
		req = req2
	}

	cc, err := t.clientConn()
	if err != nil {
		return nil, fmt.Errorf("getting cgi_h2c client connection: %v", err)
	}
	resp, err := cc.RoundTrip(req)
	if err != nil {
		if t.restartEnabled() {
			t.markBroken()
		}
		return nil, err
	}
	return resp, nil
}

func (t *Transport) clientConn() (*http2.ClientConn, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for t.stopping {
		t.cond.Wait()
	}

	if t.cc != nil && t.cc.CanTakeNewRequest() {
		return t.cc, nil
	}

	state := t.detachLocked()
	if !state.empty() {
		_ = t.closeDetachedState(state, "closing stale cgi_h2c session")
	}

	for t.stopping {
		t.cond.Wait()
	}

	if t.cc != nil && t.cc.CanTakeNewRequest() {
		return t.cc, nil
	}
	if t.lastStartFailure != nil && time.Since(t.lastStartFailureTime) < startupFailureCooldown {
		return nil, fmt.Errorf("recent cgi_h2c startup failure: %v", t.lastStartFailure)
	}
	if err := t.startLocked(); err != nil {
		t.lastStartFailure = err
		t.lastStartFailureTime = time.Now()
		return nil, err
	}
	t.lastStartFailure = nil
	t.lastStartFailureTime = time.Time{}
	return t.cc, nil
}

func (t *Transport) startLocked() error {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, t.Command, t.Args...)
	configureBackendProcAttrs(cmd)
	cmd.Dir = t.Dir
	cmd.Env = t.processEnv()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("creating stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("creating stdout pipe: %v", err)
	}
	var stderr io.ReadCloser
	if t.CaptureStderr {
		stderr, err = cmd.StderrPipe()
		if err != nil {
			cancel()
			return fmt.Errorf("creating stderr pipe: %v", err)
		}
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("starting backend command: %v", err)
	}

	var stderrWG sync.WaitGroup
	if stderr != nil {
		stderrWG.Add(1)
		go func() {
			defer stderrWG.Done()
			if err := logStderr(stderr, t.logger); err != nil && t.logger != nil {
				t.logger.Warn("reading cgi_h2c child stderr", zap.Error(err))
			}
		}()
	}

	child := &childProcess{
		cmd:    cmd,
		cancel: cancel,
		waitCh: make(chan error, 1),
	}
	go func() {
		err := cmd.Wait()
		stderrWG.Wait()
		child.waitCh <- err
		close(child.waitCh)
	}()

	conn := &cgiConn{
		r:     stdout,
		w:     stdin,
		close: child.cancel,
	}
	h2Transport := http2.Transport{AllowHTTP: true}
	cc, err := h2Transport.NewClientConn(conn)
	if err != nil {
		_ = conn.Close()
		_ = stopChild(child, time.Duration(t.ShutdownTimeout))
		return fmt.Errorf("creating HTTP/2 client connection: %v", err)
	}

	t.child = child
	t.conn = conn
	t.cc = cc
	return nil
}

func (t *Transport) processEnv() []string {
	if len(t.EnvVars) == 0 {
		return nil
	}

	repl := caddy.NewReplacer()
	env := os.Environ()
	keys := make([]string, 0, len(t.EnvVars))
	for key := range t.EnvVars {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		env = append(env, key+"="+repl.ReplaceAll(t.EnvVars[key], ""))
	}
	return env
}

func (t *Transport) markBroken() {
	t.mu.Lock()
	defer t.mu.Unlock()

	for t.stopping {
		t.cond.Wait()
	}
	state := t.detachLocked()
	_ = t.closeDetachedState(state, "closing broken cgi_h2c session")
}

// Cleanup closes the h2c session and stops the child process.
func (t *Transport) Cleanup() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	for t.stopping {
		t.cond.Wait()
	}
	state := t.detachLocked()
	return t.closeDetachedState(state, "closing cgi_h2c session during cleanup")
}

type transportState struct {
	cc    *http2.ClientConn
	conn  net.Conn
	child *childProcess
}

func (s transportState) empty() bool {
	return s.cc == nil && s.conn == nil && s.child == nil
}

func (t *Transport) detachLocked() transportState {
	state := transportState{cc: t.cc, conn: t.conn, child: t.child}
	t.cc = nil
	t.conn = nil
	t.child = nil
	return state
}

func (t *Transport) closeState(state transportState) error {
	var err error
	if state.cc != nil {
		err = errors.Join(err, state.cc.Close())
	}
	if state.conn != nil {
		err = errors.Join(err, state.conn.Close())
	}
	if state.child != nil {
		err = errors.Join(err, stopChild(state.child, time.Duration(t.ShutdownTimeout)))
	}
	return err
}

func (t *Transport) closeDetachedState(state transportState, logMsg string) error {
	if state.empty() {
		return nil
	}

	t.stopping = true
	t.mu.Unlock()
	err := t.closeState(state)
	t.mu.Lock()
	t.stopping = false
	t.cond.Broadcast()

	if err != nil && t.logger != nil && logMsg != "" {
		t.logger.Debug(logMsg, zap.Error(err))
	}
	return err
}

func (t *Transport) restartEnabled() bool {
	return t.Restart == nil || *t.Restart
}

var (
	// Interface guards.
	_ caddy.Provisioner  = (*Transport)(nil)
	_ caddy.CleanerUpper = (*Transport)(nil)
	_ http.RoundTripper  = (*Transport)(nil)
)
