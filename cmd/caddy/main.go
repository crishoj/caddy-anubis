// Command caddy is a development binary that builds Caddy with the
// caddy-anubis middleware compiled in. Useful for running the integration
// spike without xcaddy:
//
//	go run ./cmd/caddy run --config Caddyfile
package main

import (
	caddycmd "github.com/caddyserver/caddy/v2/cmd"

	// Standard Caddy modules: file_server, reverse_proxy, respond, etc.
	_ "github.com/caddyserver/caddy/v2/modules/standard"

	// The caddy-anubis middleware.
	_ "github.com/crishoj/caddy-anubis"
)

func main() {
	caddycmd.Main()
}
