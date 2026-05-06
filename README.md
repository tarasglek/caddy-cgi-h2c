# Caddy CGI H2C: Modern successor to cgi-bin

I was curious if we could marry cgi-bin process model with reverse proxies. We can! This caddy module allows one to reverse proxy to a binary via stdio. Eg your server is a binary that receives unencrypted http/2 on stdin and responds on stdout. Since http/2 supports connection multiplexing, a single binary invocation can handle multiple requests. After last request is done, the process is shut down.

This means less work to deploy services:
- No need to allocate ports/ips
- No need to write systemd units or docker services to manage services

Processes are easy to sandbox:
- Can use [landrun](https://github.com/Zouuup/landrun) or some BSD equivalent
- Can wrap your binary in wasm runtime

## Caddy module name

```text
http.reverse_proxy.transport.cgi_stdio_h2c
```

## Build

Install [`xcaddy`](https://github.com/caddyserver/xcaddy), then build Caddy with this plugin:

```sh
xcaddy build --with github.com/tarasglek/caddy-cgi-h2c
```

For local development from this repository:

```sh
xcaddy build --with github.com/tarasglek/caddy-cgi-h2c=.
```

## Caddyfile example

```caddyfile
:8080

reverse_proxy 127.0.0.1:65535 {
	transport cgi_stdio_h2c {
		command /usr/bin/example-h2c
		args --stdio --flag value
		dir /srv/app
		env KEY value
		restart true
		capture_stderr
		shutdown_timeout 2s
	}
}
```

The upstream address is still required by Caddy's reverse proxy handler, but this transport communicates with the configured child process over stdio.

## JSON example

```json
{
  "apps": {
    "http": {
      "servers": {
        "srv0": {
          "listen": [":8080"],
          "routes": [
            {
              "handle": [
                {
                  "handler": "reverse_proxy",
                  "transport": {
                    "protocol": "cgi_stdio_h2c",
                    "command": "/usr/bin/example-h2c",
                    "args": ["--stdio"],
                    "capture_stderr": true,
                    "shutdown_timeout": 2000000000
                  },
                  "upstreams": [{"dial": "127.0.0.1:65535"}]
                }
              ]
            }
          ]
        }
      }
    }
  }
}
```

## Configuration reference

```caddyfile
transport cgi_stdio_h2c {
	command <path>
	args <arg...>
	dir <path>
	env <key> <value>
	restart <bool>
	capture_stderr
	shutdown_timeout <duration>
}
```

- `command` — executable path for the backend process. Required.
- `args` — arguments passed to `command`.
- `dir` — working directory for the backend process.
- `env` — extra environment variable. May be repeated. Values may use Caddy placeholders.
- `restart` — whether to discard and recreate the backend session after a `RoundTrip` error. Default: `true`.
- `capture_stderr` — capture child stderr and write each line to Caddy logs.
- `shutdown_timeout` — time to wait for graceful shutdown before killing the process. Default: `2s`.

## Backend protocol expectations

The child process must speak cleartext HTTP/2 over stdin/stdout. Caddy writes HTTP/2 client frames to the child's stdin and reads HTTP/2 server frames from the child's stdout.

## Operational notes

- Each transport instance uses one shared HTTP/2 session to the child process.
- Concurrent requests are multiplexed as HTTP/2 streams on that session.
- If `restart` is enabled, a request error marks the session broken and starts a new process for later requests.
- Restarting the shared session may affect other in-flight streams.
- `Cleanup` stops the current child process; a later request after reprovisioning starts a new process.
- On Unix systems, shutdown signals are sent to the child's process group.
- On Windows, shutdown kills the process directly.

## License

Apache-2.0.
