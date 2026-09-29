package layr8

import (
	"fmt"
	"log/slog"
)

// apiKeyHeader is the request header that carries the API key, on both the
// plugin socket's WebSocket upgrade and every REST call. The key is never put
// in a URL: URLs are what proxies, load balancers and request logs record.
const apiKeyHeader = "x-api-key"

// redacted is what every formatted form of a Config or Client prints in place
// of the API key.
const redacted = "[REDACTED]"

// String renders the Config with the API key redacted, so that logging a
// Config — fmt.Printf("%v", cfg), slog.Any("cfg", cfg) — never prints the key.
func (c Config) String() string {
	key := ""
	if c.APIKey != "" {
		key = redacted
	}
	return fmt.Sprintf("layr8.Config{NodeURL:%q APIKey:%s AgentDID:%q ParentDID:%q Persistent:%t}",
		c.NodeURL, key, c.AgentDID, c.ParentDID, c.Persistent)
}

// GoString covers %#v, which would otherwise print every field verbatim.
func (c Config) GoString() string { return c.String() }

// Format covers every other verb (%d, %x, ...), each of which would otherwise
// walk the struct and print the key.
func (c Config) Format(f fmt.State, _ rune) { fmt.Fprint(f, c.String()) }

// LogValue covers structured loggers that do not go through fmt.
func (c Config) LogValue() slog.Value { return slog.StringValue(c.String()) }

// String renders the Client without its configuration's API key. Without it,
// fmt would walk the unexported fields (cfg, transport, rest) and print the
// key from each of them.
func (c *Client) String() string {
	if c == nil {
		return "layr8.Client(nil)"
	}
	return fmt.Sprintf("layr8.Client{DID:%q NodeURL:%q}", c.agentDID, c.cfg.NodeURL)
}

// GoString covers %#v.
func (c *Client) GoString() string { return c.String() }

// Format covers every other verb.
func (c *Client) Format(f fmt.State, _ rune) { fmt.Fprint(f, c.String()) }

// LogValue covers structured loggers that do not go through fmt.
func (c *Client) LogValue() slog.Value { return slog.StringValue(c.String()) }
