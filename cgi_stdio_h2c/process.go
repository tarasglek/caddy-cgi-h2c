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

package cgistdioh2c

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"
)

const maxStderrLogLine = 16 * 1024

type childProcess struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	waitCh chan error
}

var stopChild = stopChildProcess

func stopChildProcess(child *childProcess, timeout time.Duration) error {
	if child == nil {
		return nil
	}
	child.cancel()
	if child.cmd.Process != nil {
		_ = terminateProcessGroup(child.cmd.Process, syscall.SIGTERM)
	}

	select {
	case err := <-child.waitCh:
		return expectedStopError(err)
	case <-time.After(timeout):
		if child.cmd.Process != nil {
			_ = terminateProcessGroup(child.cmd.Process, syscall.SIGKILL)
			_ = child.cmd.Process.Kill()
		}
		err := <-child.waitCh
		return expectedStopError(err)
	}
}

func expectedStopError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return err
}

func logStderr(r io.Reader, logger *zap.Logger) error {
	br := bufio.NewReaderSize(r, maxStderrLogLine)
	for {
		line, err := br.ReadSlice('\n')
		if len(line) > 0 && logger != nil {
			fields := []zap.Field{zap.String("line", strings.TrimRight(string(line), "\r\n"))}
			if errors.Is(err, bufio.ErrBufferFull) {
				fields = append(fields, zap.Bool("truncated", true))
			}
			logger.Warn("cgi_stdio_h2c child stderr", fields...)
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, bufio.ErrBufferFull) {
				if errors.Is(err, bufio.ErrBufferFull) {
					continue
				}
				return nil
			}
			return err
		}
	}
}
