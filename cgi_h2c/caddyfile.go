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
	"strconv"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

// UnmarshalCaddyfile deserializes Caddyfile tokens into t.
//
//	transport cgi_h2c {
//	    command <path>
//	    args <arg...>
//	    dir <path>
//	    env <key> <value>
//	    restart <bool>
//	    capture_stderr
//	    shutdown_timeout <duration>
//	}
func (t *Transport) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	d.Next() // consume transport name
	for d.NextBlock(0) {
		switch d.Val() {
		case "command":
			if !d.NextArg() {
				return d.ArgErr()
			}
			t.Command = d.Val()
			if d.NextArg() {
				return d.ArgErr()
			}
		case "args":
			t.Args = d.RemainingArgs()
			if len(t.Args) == 0 {
				return d.ArgErr()
			}
		case "dir":
			if !d.NextArg() {
				return d.ArgErr()
			}
			t.Dir = d.Val()
			if d.NextArg() {
				return d.ArgErr()
			}
		case "env":
			if !d.NextArg() {
				return d.ArgErr()
			}
			key := d.Val()
			if !d.NextArg() {
				return d.ArgErr()
			}
			if t.EnvVars == nil {
				t.EnvVars = make(map[string]string)
			}
			t.EnvVars[key] = d.Val()
			if d.NextArg() {
				return d.ArgErr()
			}
		case "restart":
			if !d.NextArg() {
				return d.ArgErr()
			}
			restart, err := strconv.ParseBool(d.Val())
			if err != nil {
				return d.Errf("invalid restart value %q: %v", d.Val(), err)
			}
			t.Restart = &restart
			if d.NextArg() {
				return d.ArgErr()
			}
		case "capture_stderr":
			if d.NextArg() {
				return d.ArgErr()
			}
			t.CaptureStderr = true
		case "shutdown_timeout":
			if !d.NextArg() {
				return d.ArgErr()
			}
			dur, err := caddy.ParseDuration(d.Val())
			if err != nil {
				return d.Errf("bad timeout value %s: %v", d.Val(), err)
			}
			t.ShutdownTimeout = caddy.Duration(dur)
			if d.NextArg() {
				return d.ArgErr()
			}
		default:
			return d.Errf("unrecognized subdirective: %s", d.Val())
		}
	}
	return nil
}

var (
	// Interface guards.
	_ caddyfile.Unmarshaler = (*Transport)(nil)
)
