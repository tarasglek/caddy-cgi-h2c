# Caddy CGI stdio h2c Reverse Proxy Plugin Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Extract the branch's stdio h2c reverse proxy transport into a standalone Caddy plugin published as `github.com/tarasglek/caddy-cgi-h2c`.

**Architecture:** The repository root is an xcaddy-friendly shim package that blank-imports the real transport module in subdirectory `cgi_stdio_h2c`. The transport remains a Caddy reverse proxy transport, renamed to `http.reverse_proxy.transport.cgi_stdio_h2c`, with matching Caddyfile syntax `transport cgi_stdio_h2c`.

**Tech Stack:** Go, Caddy v2 module APIs, `golang.org/x/net/http2`, GitHub Actions, xcaddy.

---

### Task 1: Scaffold standalone repository

**Files:**
- Create: `/home/taras/Documents/caddy-cgi-h2c/go.mod`
- Create: `/home/taras/Documents/caddy-cgi-h2c/imports.go`
- Create: `/home/taras/Documents/caddy-cgi-h2c/cgi_stdio_h2c/*.go`

**Steps:**
1. Initialize git repository if missing.
2. Create `go.mod` with module path `github.com/tarasglek/caddy-cgi-h2c`.
3. Copy branch implementation files into `cgi_stdio_h2c/`.
4. Add root `imports.go` that blank-imports `github.com/tarasglek/caddy-cgi-h2c/cgi_stdio_h2c`.
5. Run `go test ./...` and expect module setup/downloads to complete or expose rename errors.

### Task 2: Rename module and docs-facing identifiers

**Files:**
- Modify: `/home/taras/Documents/caddy-cgi-h2c/cgi_stdio_h2c/*.go`

**Steps:**
1. Rename package declarations to `cgistdioh2c`.
2. Change Caddy module ID to `http.reverse_proxy.transport.cgi_stdio_h2c`.
3. Change test Caddyfile snippets from `stdio_h2c` to `cgi_stdio_h2c`.
4. Rename environment/test helper identifiers and log/error text from `stdio_h2c`/`STDIO_H2C` to `cgi_stdio_h2c`/`CGI_STDIO_H2C`.
5. Run `gofmt -w .`.
6. Run `go test ./...` and expect pass.

### Task 3: Add plugin README, Dockerfile, and CI

**Files:**
- Create: `/home/taras/Documents/caddy-cgi-h2c/README.md`
- Create: `/home/taras/Documents/caddy-cgi-h2c/LICENSE`
- Create: `/home/taras/Documents/caddy-cgi-h2c/Dockerfile`
- Create: `/home/taras/Documents/caddy-cgi-h2c/.github/workflows/ci.yml`
- Create: `/home/taras/Documents/caddy-cgi-h2c/.gitignore`

**Steps:**
1. Document module name, build command, Caddyfile example, JSON example, configuration reference, and operational notes.
2. Add Apache-2.0 license matching source headers.
3. Add Dockerfile using `caddy:builder` and `xcaddy build --with github.com/tarasglek/caddy-cgi-h2c`.
4. Add GitHub Actions workflow running `go test -race ./...` and an xcaddy build on Linux/macOS/Windows.
5. Run `go test -race ./...`.
6. Run local `xcaddy build --with github.com/tarasglek/caddy-cgi-h2c=.`.

### Task 4: Publish to GitHub

**Files:**
- Existing repository files

**Steps:**
1. Commit all files.
2. Create GitHub repository `tarasglek/caddy-cgi-h2c` if it does not exist.
3. Add remote `origin`.
4. Push default branch.
5. Verify `gh repo view tarasglek/caddy-cgi-h2c` works.
