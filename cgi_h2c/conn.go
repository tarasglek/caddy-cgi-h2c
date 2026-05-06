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
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

type cgiConn struct {
	r     io.ReadCloser
	w     io.WriteCloser
	close func()

	closeOnce sync.Once
	closeErr  error
}

func (c *cgiConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *cgiConn) Write(p []byte) (int, error) { return c.w.Write(p) }

func (c *cgiConn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = ignoreClosedPipeError(errors.Join(c.r.Close(), c.w.Close()))
		if c.close != nil {
			c.close()
		}
	})
	return c.closeErr
}

func ignoreClosedPipeError(err error) error {
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}

func (c *cgiConn) LocalAddr() net.Addr  { return cgiAddr("local") }
func (c *cgiConn) RemoteAddr() net.Addr { return cgiAddr("remote") }

// Deadlines are not implemented for stdin/stdout pipes. Request cancellation closes
// the underlying connection/process instead.
func (c *cgiConn) SetDeadline(time.Time) error { return nil }

func (c *cgiConn) SetReadDeadline(time.Time) error  { return nil }
func (c *cgiConn) SetWriteDeadline(time.Time) error { return nil }

type cgiAddr string

func (a cgiAddr) Network() string { return "cgi-h2c" }
func (a cgiAddr) String() string  { return "cgi-h2c-" + string(a) }
