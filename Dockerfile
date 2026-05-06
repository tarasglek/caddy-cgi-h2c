FROM caddy:builder AS builder

RUN xcaddy build \
    --with github.com/tarasglek/caddy-cgi-h2c

FROM caddy:latest

COPY --from=builder /usr/bin/caddy /usr/bin/caddy
