package layr8

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds the configuration for a Layr8 client.
type Config struct {
	// NodeURL is the WebSocket URL of the Layr8 cloud-node.
	// Fallback: LAYR8_NODE_URL environment variable.
	NodeURL string

	// APIKey is the authentication key for the cloud-node.
	// Fallback: LAYR8_API_KEY environment variable.
	APIKey string

	// AgentDID is the DID identity of this agent — the address other
	// agents use to message it. Required: the cloud-node rejects a
	// connection that doesn't specify a DID.
	// Fallback: LAYR8_AGENT_DID environment variable.
	AgentDID string

	// Persistent, when true, tells the cloud-node to persist the DID
	// and its keys across restarts. Use this for agents that need
	// stable key material for cross-node DIDComm (e.g., services
	// that send messages to agents on other nodes).
	// Default: false (ephemeral storage).
	Persistent bool

	// Protocols lists additional protocol URIs to advertise on join.
	// Use this for sender-only actors that need to declare protocols
	// without registering handlers. Merged with handler-derived protocols.
	Protocols []string

	// DialContext, if set, overrides the default TCP dialer for the
	// WebSocket connection. Use this for custom network transports
	// (e.g., vsock in Nitro Enclaves).
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)

	// AttachGrants controls whether the Verifiable Grants covering each
	// outbound message are attached automatically. Nil means ON.
	// Fallback: LAYR8_ATTACH_GRANTS env ("false"/"0" turns it off).
	//
	// The node requires a grant for anything its policy does not allow
	// outright. Before this existed nothing in this SDK attached one —
	// which is what produced "no grant covers this call" denials that read
	// as a misconfigured grant rather than an absent one. On by default:
	// opting IN would have left every existing agent in exactly the state
	// that cost two teams days. See wallet.go.
	AttachGrants *bool

	// GrantCacheTTL is how long held grants are cached before re-reading.
	// Zero means the default (60s). Fallback: LAYR8_GRANT_CACHE_MS env.
	GrantCacheTTL time.Duration

	// GrantReadTimeout bounds one credential read. Zero means the default
	// (2s). Fallback: LAYR8_GRANT_READ_TIMEOUT_MS env.
	//
	// The read sits in front of every send, so an unbounded one against a
	// node that accepted the connection and went quiet stalls the send
	// itself. A lapsed deadline is an ordinary read error: the message goes
	// out unattached and OnGrantMiss says so.
	//
	// Unlike RESTTimeout, this cannot be disabled: a zero deadline would
	// abort every read before it started, turning a mistyped variable into
	// an agent that attaches nothing at all — the exact failure the whole
	// feature exists to end.
	GrantReadTimeout time.Duration

	// RESTTimeout bounds every other REST call (credential and presentation
	// sign/verify/store/list). Zero means the default (30s); a negative
	// value disables the deadline. Fallback: LAYR8_REST_TIMEOUT_MS env.
	RESTTimeout time.Duration

	// OnGrantMiss is called when a message went out with NO covering grant
	// and the node then denied it, when the covering set had to be capped,
	// or when the grants could not be read at all. See GrantMissInfo.
	//
	// Wire this to a log. The node's denial names the grant it could not
	// find, which sends people to check a grant that is fine; only the
	// sender knows no credential was ever on the wire.
	OnGrantMiss func(GrantMissInfo)
}

// GrantMissInfo is what Config.OnGrantMiss is told.
//
// Exactly one of DenialCode, Capped and Err is set, and which one says what
// happened:
//
//   - DenialCode — the node denied a message we sent with NOTHING ATTACHED.
//     This is the case the callback exists for.
//   - Capped — more grants covered the message than fit on it. The policy
//     allows on the first passing grant, so the one that mattered may be
//     among those left off.
//   - Err — the grants could not be READ at all. Never a normal outcome:
//     every send after it is flying blind.
type GrantMissInfo struct {
	To   []string
	Type string

	DenialCode string
	Capped     *GrantCapInfo
	Err        error
}

// GrantCapInfo reports how many covering credentials there were and how many
// fit.
type GrantCapInfo struct {
	Covering int
	Attached int
}

// Default grant and REST deadlines.
//
// DefaultGrantReadTimeout is two seconds, chosen from both ends: it is a JSON
// GET to the same node this client already holds a WebSocket to, so the honest
// answer arrives in milliseconds — two seconds survives a cold node, a warming
// connection pool and a GC pause. And it sits comfortably under the wallet's 5s
// failure cache, which is measured from the START of the read; a deadline at or
// above that TTL would leave the failure lapsed the moment it was recorded, so
// every send would pay the full deadline instead of one per window.
//
// DefaultRESTTimeout is thirty seconds, and the number matters less than what it
// is measured on: the whole call, so the node's own signing time counts against
// it. 30s is far above any honest sign against a node this client already holds
// a WebSocket to, and far below any duration a person watching would still call
// "working" rather than "hung".
const (
	DefaultGrantCacheTTL    = 60 * time.Second
	DefaultGrantReadTimeout = 2 * time.Second
	DefaultRESTTimeout      = 30 * time.Second
)

// resolveConfig fills empty fields from environment variables and validates required fields.
func resolveConfig(cfg Config) (Config, error) {
	if cfg.NodeURL == "" {
		cfg.NodeURL = os.Getenv("LAYR8_NODE_URL")
	}
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("LAYR8_API_KEY")
	}
	if cfg.AgentDID == "" {
		cfg.AgentDID = os.Getenv("LAYR8_AGENT_DID")
	}

	if cfg.NodeURL == "" {
		return cfg, fmt.Errorf("NodeURL is required (set in Config or LAYR8_NODE_URL env)")
	}

	// Normalize HTTP(S) URLs to WebSocket scheme.
	// In production, the /plugin_socket endpoint serves WebSocket over HTTPS.
	if rest, ok := strings.CutPrefix(cfg.NodeURL, "https://"); ok {
		cfg.NodeURL = "wss://" + rest
	} else if rest, ok := strings.CutPrefix(cfg.NodeURL, "http://"); ok {
		cfg.NodeURL = "ws://" + rest
	}
	if cfg.APIKey == "" {
		return cfg, fmt.Errorf("APIKey is required (set in Config or LAYR8_API_KEY env)")
	}

	if cfg.AttachGrants == nil {
		attach := envBool("LAYR8_ATTACH_GRANTS", true)
		cfg.AttachGrants = &attach
	}
	cfg.GrantCacheTTL = envDuration(cfg.GrantCacheTTL, "LAYR8_GRANT_CACHE_MS", DefaultGrantCacheTTL, 0)
	// Minimum 1ms: see Config.GrantReadTimeout for why zero is not a way to
	// disable this one.
	cfg.GrantReadTimeout = envDuration(cfg.GrantReadTimeout, "LAYR8_GRANT_READ_TIMEOUT_MS", DefaultGrantReadTimeout, time.Millisecond)
	cfg.RESTTimeout = envDuration(cfg.RESTTimeout, "LAYR8_REST_TIMEOUT_MS", DefaultRESTTimeout, 0)

	return cfg, nil
}

// envBool reads an environment boolean spelled the way operators spell them.
// Anything unrecognised — including the empty string an unset-but-exported
// variable produces — leaves the default alone, rather than reading as false.
func envBool(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

// envDuration resolves a millisecond setting from the explicit config or the
// environment.
//
// A non-numeric or out-of-range value is IGNORED rather than silently leaving
// the call unbounded. A NEGATIVE explicit value means "no deadline" and is
// passed through as zero, which is how a caller disables one that the zero value
// cannot express (zero means "unset, take the default"). minimum binds the
// explicit value too, not just the env one — the caller is where a bad value
// actually comes from.
func envDuration(explicit time.Duration, key string, fallback, minimum time.Duration) time.Duration {
	if explicit < 0 {
		if minimum > 0 {
			return fallback
		}
		return 0
	}
	if explicit > 0 {
		if explicit < minimum {
			return fallback
		}
		return explicit
	}

	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ms < 0 {
		return fallback
	}
	d := time.Duration(ms) * time.Millisecond
	if d < minimum {
		return fallback
	}
	return d
}

// restURLFromWebSocket derives the REST API base URL from a WebSocket URL.
// ws://alice-test.localhost/plugin_socket/websocket → http://alice-test.localhost
// wss://alice-test.localhost/plugin_socket/websocket → https://alice-test.localhost
func restURLFromWebSocket(wsURL string) string {
	u, err := url.Parse(wsURL)
	if err != nil {
		// Fallback: simple scheme replacement, strip path
		s := strings.Replace(wsURL, "wss://", "https://", 1)
		s = strings.Replace(s, "ws://", "http://", 1)
		if i := strings.Index(s, "/"); i > 8 { // after scheme://
			s = s[:i]
		}
		return s
	}

	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	default:
		u.Scheme = "http"
	}
	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
