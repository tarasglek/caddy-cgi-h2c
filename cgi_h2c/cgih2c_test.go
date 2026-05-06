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
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"golang.org/x/net/http2"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

func TestMain(m *testing.M) {
	if os.Getenv("CADDY_CGI_H2C_CHILD") == "1" {
		runChild()
		return
	}
	os.Exit(m.Run())
}

func TestUnmarshalCaddyfile(t *testing.T) {
	d := caddyfile.NewTestDispenser(`cgi_h2c {
		command /path/to/http2stdin
		args -cgi-h2c -flag value
		dir /tmp
		env KEY value
		env OTHER thing
		restart false
		capture_stderr
		shutdown_timeout 5s
	}`)

	var tr Transport
	if err := tr.UnmarshalCaddyfile(d); err != nil {
		t.Fatalf("UnmarshalCaddyfile() error = %v", err)
	}

	if tr.Command != "/path/to/http2stdin" {
		t.Errorf("Command = %q", tr.Command)
	}
	if !reflect.DeepEqual(tr.Args, []string{"-cgi-h2c", "-flag", "value"}) {
		t.Errorf("Args = %#v", tr.Args)
	}
	if tr.Dir != "/tmp" {
		t.Errorf("Dir = %q", tr.Dir)
	}
	if !reflect.DeepEqual(tr.EnvVars, map[string]string{"KEY": "value", "OTHER": "thing"}) {
		t.Errorf("EnvVars = %#v", tr.EnvVars)
	}
	if tr.Restart == nil || *tr.Restart {
		t.Errorf("Restart = %v", tr.Restart)
	}
	if !tr.CaptureStderr {
		t.Errorf("CaptureStderr = false")
	}
	if tr.ShutdownTimeout != caddy.Duration(5*time.Second) {
		t.Errorf("ShutdownTimeout = %v", tr.ShutdownTimeout)
	}
}

func TestProvisionRequiresCommand(t *testing.T) {
	var tr Transport
	if err := tr.Provision(caddy.Context{}); err == nil {
		t.Fatal("Provision() expected error for empty command")
	}
}

func TestProvisionDefaultsShutdownTimeout(t *testing.T) {
	tr := Transport{Command: os.Args[0]}
	if err := tr.Provision(caddy.Context{}); err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	if tr.ShutdownTimeout != caddy.Duration(2*time.Second) {
		t.Fatalf("ShutdownTimeout = %v, want 2s", tr.ShutdownTimeout)
	}
}

func TestProvisionRejectsNegativeShutdownTimeout(t *testing.T) {
	tr := Transport{Command: os.Args[0], ShutdownTimeout: caddy.Duration(-time.Second)}
	if err := tr.Provision(caddy.Context{}); err == nil {
		t.Fatal("Provision() expected error for negative shutdown_timeout")
	}
}

func TestProvisionRejectsInvalidEnvKeys(t *testing.T) {
	for _, key := range []string{"", "BAD=KEY"} {
		tr := Transport{Command: os.Args[0], EnvVars: map[string]string{key: "value"}}
		if err := tr.Provision(caddy.Context{}); err == nil {
			t.Fatalf("Provision() expected error for env key %q", key)
		}
	}
}

func TestLogStderrDrainsLongLines(t *testing.T) {
	longLine := strings.Repeat("x", 128*1024) + "\n"
	if err := logStderr(io.NopCloser(strings.NewReader(longLine)), zap.NewNop()); err != nil {
		t.Fatalf("logStderr() error = %v", err)
	}
}

func TestRoundTripWrapsStartError(t *testing.T) {
	tr := &Transport{Command: "caddy-cgi-h2c-command-that-does-not-exist"}
	if err := tr.Provision(caddy.Context{}); err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	_, err := roundTripBody(context.Background(), tr, "/")
	if err == nil {
		t.Fatal("RoundTrip() expected error")
	}
	if !strings.Contains(err.Error(), "getting cgi_h2c client connection") || !strings.Contains(err.Error(), "starting backend command") {
		t.Fatalf("RoundTrip() error = %v, want client connection and starting backend command context", err)
	}
}

func TestClientConnThrottlesRepeatedStartFailures(t *testing.T) {
	tr := &Transport{Command: "caddy-cgi-h2c-command-that-does-not-exist"}
	if err := tr.Provision(caddy.Context{}); err != nil {
		t.Fatalf("Provision() error = %v", err)
	}

	_, err1 := roundTripBody(context.Background(), tr, "/")
	if err1 == nil {
		t.Fatal("first RoundTrip() expected error")
	}
	_, err2 := roundTripBody(context.Background(), tr, "/")
	if err2 == nil {
		t.Fatal("second RoundTrip() expected error")
	}
	if !strings.Contains(err2.Error(), "recent cgi_h2c startup failure") {
		t.Fatalf("second error = %v, want cached startup failure context", err2)
	}
}

func TestStdioConnImplementsNetConn(t *testing.T) {
	var _ net.Conn = (*cgiConn)(nil)
}

func TestRoundTripMultiplexesOverOneChildStdioH2CSession(t *testing.T) {
	tr := &Transport{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--"},
		EnvVars: map[string]string{"CADDY_CGI_H2C_CHILD": "1"},
	}
	if err := tr.Provision(caddy.Context{}); err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	defer tr.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	longDone := make(chan string, 1)
	longErr := make(chan error, 1)
	go func() {
		body, err := roundTripBody(ctx, tr, "/long-req/start")
		if err != nil {
			longErr <- err
			return
		}
		longDone <- body
	}()

	smallBody, err := roundTripBody(ctx, tr, "/small")
	if err != nil {
		t.Fatalf("/small RoundTrip() error = %v", err)
	}
	if !strings.Contains(smallBody, "proto=HTTP/2.0") {
		t.Fatalf("/small response = %q", smallBody)
	}

	select {
	case body := <-longDone:
		t.Fatalf("/long-req/start completed before /long-req/finish: %q", body)
	case err := <-longErr:
		t.Fatalf("/long-req/start failed before /long-req/finish: %v", err)
	default:
	}

	finishBody, err := roundTripBody(ctx, tr, "/long-req/finish")
	if err != nil {
		t.Fatalf("/long-req/finish RoundTrip() error = %v", err)
	}
	if !strings.Contains(finishBody, "proto=HTTP/2.0") {
		t.Fatalf("/long-req/finish response = %q", finishBody)
	}

	select {
	case body := <-longDone:
		if !strings.Contains(body, "proto=HTTP/2.0") {
			t.Fatalf("/long-req/start response = %q", body)
		}
	case err := <-longErr:
		t.Fatalf("/long-req/start RoundTrip() error = %v", err)
	case <-ctx.Done():
		t.Fatal("timeout waiting for /long-req/start to finish")
	}
}

func TestCleanupTerminatesChildProcess(t *testing.T) {
	tr := newTestTransport(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pid := roundTripPID(t, ctx, tr)
	if err := tr.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	assertProcessGone(t, pid)
}

func TestCleanupIsIdempotent(t *testing.T) {
	tr := newTestTransport(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_ = roundTripPID(t, ctx, tr)
	if err := tr.Cleanup(); err != nil {
		t.Fatalf("first Cleanup() error = %v", err)
	}
	if err := tr.Cleanup(); err != nil {
		t.Fatalf("second Cleanup() error = %v", err)
	}
}

func TestCleanupAfterChildExitIsIdempotent(t *testing.T) {
	tr := &Transport{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--"},
		EnvVars: map[string]string{"CADDY_CGI_H2C_CHILD": "1", "CADDY_CGI_H2C_EXIT_AFTER_PID": "1"},
	}
	if err := tr.Provision(caddy.Context{}); err != nil {
		t.Fatalf("Provision() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_ = roundTripPID(t, ctx, tr)
	time.Sleep(100 * time.Millisecond)

	if err := tr.Cleanup(); err != nil {
		t.Fatalf("first Cleanup() error = %v", err)
	}
	if err := tr.Cleanup(); err != nil {
		t.Fatalf("second Cleanup() error = %v", err)
	}
}

func TestRoundTripAfterCleanupRestartsChild(t *testing.T) {
	tr := newTestTransport(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pid1 := roundTripPID(t, ctx, tr)
	if err := tr.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	pid2 := roundTripPID(t, ctx, tr)
	defer tr.Cleanup()

	if runtime.GOOS != "windows" && pid1 == pid2 {
		t.Fatalf("pid after restart = %d, want different pid", pid2)
	}
}

func TestCleanupDoesNotHoldMutexWhileStoppingChild(t *testing.T) {
	tr := newTestTransport(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_ = roundTripPID(t, ctx, tr)

	entered := make(chan struct{})
	unblock := make(chan struct{})

	originalStopChild := stopChild
	stopChild = func(child *childProcess, timeout time.Duration) error {
		close(entered)
		<-unblock
		return originalStopChild(child, timeout)
	}
	defer func() { stopChild = originalStopChild }()

	cleanupDone := make(chan error, 1)
	go func() { cleanupDone <- tr.Cleanup() }()

	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("timeout waiting for Cleanup to begin stopping child")
	}

	lockDone := make(chan struct{})
	go func() {
		tr.mu.Lock()
		tr.mu.Unlock()
		close(lockDone)
	}()

	select {
	case <-lockDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Cleanup held transport mutex while stopping child")
	}

	close(unblock)
	if err := <-cleanupDone; err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
}

func TestRoundTripWaitsForStoppingChildBeforeRestart(t *testing.T) {
	tr := newTestTransport(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pid1 := roundTripPID(t, ctx, tr)

	entered := make(chan struct{})
	unblock := make(chan struct{})

	originalStopChild := stopChild
	stopChild = func(child *childProcess, timeout time.Duration) error {
		close(entered)
		<-unblock
		return originalStopChild(child, timeout)
	}
	defer func() { stopChild = originalStopChild }()

	cleanupDone := make(chan error, 1)
	go func() { cleanupDone <- tr.Cleanup() }()

	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("timeout waiting for Cleanup to begin stopping child")
	}

	roundTripDone := make(chan int, 1)
	roundTripErr := make(chan error, 1)
	go func() {
		pid, err := roundTripPIDValue(ctx, tr)
		if err != nil {
			roundTripErr <- err
			return
		}
		roundTripDone <- pid
	}()

	select {
	case pid := <-roundTripDone:
		t.Fatalf("RoundTrip started child %d before old child %d finished stopping", pid, pid1)
	case err := <-roundTripErr:
		t.Fatalf("RoundTrip returned before old child finished stopping: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(unblock)
	if err := <-cleanupDone; err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	select {
	case pid2 := <-roundTripDone:
		if runtime.GOOS != "windows" && pid2 == pid1 {
			t.Fatalf("pid after restart = %d, want different pid", pid2)
		}
	case err := <-roundTripErr:
		t.Fatalf("RoundTrip after stop error = %v", err)
	case <-ctx.Done():
		t.Fatal("timeout waiting for RoundTrip after stop")
	}
}

func TestHelperProcess(t *testing.T) {}

func newTestTransport(t *testing.T) *Transport {
	t.Helper()
	tr := &Transport{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--"},
		EnvVars: map[string]string{"CADDY_CGI_H2C_CHILD": "1"},
	}
	if err := tr.Provision(caddy.Context{}); err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	return tr
}

func roundTripPID(t *testing.T, ctx context.Context, rt http.RoundTripper) int {
	t.Helper()
	pid, err := roundTripPIDValue(ctx, rt)
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func roundTripPIDValue(ctx context.Context, rt http.RoundTripper) (int, error) {
	body, err := roundTripBody(ctx, rt, "/pid")
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(body))
	if err != nil {
		return 0, fmt.Errorf("invalid pid response %q: %v", body, err)
	}
	return pid, nil
}

func assertProcessGone(t *testing.T, pid int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if !processExists(pid) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d still exists", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func roundTripBody(ctx context.Context, rt http.RoundTripper, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://cgi-h2c.local"+path, nil)
	if err != nil {
		return "", err
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
	}
	return string(body), nil
}

func runChild() {
	finish := make(chan struct{})
	started := make(chan struct{})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/long-req/start":
			close(started)
			<-finish
			fmt.Fprintf(w, "long done proto=%s\n", r.Proto)
		case "/small":
			<-started
			fmt.Fprintf(w, "small proto=%s\n", r.Proto)
		case "/long-req/finish":
			close(finish)
			fmt.Fprintf(w, "finish proto=%s\n", r.Proto)
		case "/pid":
			fmt.Fprintf(w, "%d\n", os.Getpid())
			if os.Getenv("CADDY_CGI_H2C_EXIT_AFTER_PID") == "1" {
				go func() {
					time.Sleep(10 * time.Millisecond)
					os.Exit(0)
				}()
			}
		case "/block":
			select {}
		default:
			http.NotFound(w, r)
		}
	})

	conn := &cgiConn{r: os.Stdin, w: os.Stdout, close: func() {}}
	server := &http2.Server{}
	server.ServeConn(conn, &http2.ServeConnOpts{Handler: handler})
}
