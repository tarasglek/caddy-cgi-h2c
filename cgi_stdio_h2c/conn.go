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
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

type stdioConn struct {
	r     io.ReadCloser
	w     io.WriteCloser
	close func()

	closeOnce sync.Once
	closeErr  error
}

func (c *stdioConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *stdioConn) Write(p []byte) (int, error) { return c.w.Write(p) }

func (c *stdioConn) Close() error {
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

func (c *stdioConn) LocalAddr() net.Addr  { return stdioAddr("local") }
func (c *stdioConn) RemoteAddr() net.Addr { return stdioAddr("remote") }

// Deadlines are not implemented for stdio pipes. Request cancellation closes
// the underlying connection/process instead.
func (c *stdioConn) SetDeadline(time.Time) error { return nil }

func (c *stdioConn) SetReadDeadline(time.Time) error  { return nil }
func (c *stdioConn) SetWriteDeadline(time.Time) error { return nil }

type stdioAddr string

func (a stdioAddr) Network() string { return "stdio" }
func (a stdioAddr) String() string  { return "stdio-" + string(a) }
