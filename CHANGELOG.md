# Changelog

All notable changes to `github.com/layr8/go-sdk`. Format loosely follows [Keep a Changelog](https://keepachangelog.com/); versioning follows [SemVer](https://semver.org/).

This file starts here. Earlier releases are recorded only in git history.

## [Unreleased]

### Added

- `IdentityAttachment(credentialJWS)` — a first-class way to attach an
  **identity credential** (a credential about who the sender is, with no
  `credentialSubject.scope`) so it reaches the cloud-node's
  `sender_credentials` policy input, where a grant's `senderCredentials`
  requirement can see it. It builds the attachment; **the caller names the
  credential**. The SDK does not choose: the requirement being satisfied lives
  in the recipient's grant and never reaches the sender, so automatic selection
  could only mean "attach everything the holder has", which is a disclosure
  decision, not a convenience. Returns `ErrNotCompactJWS` for anything that is
  not a compact JWS, and `ErrCredentialIsGrant` for a credential that carries a
  scope — that is a grant, and attached this way it would be routed as one and
  satisfy nothing.
- `IsIdentityAttachment(attachment)`, the same test applied to an attachment
  already on a message, and `CredentialMediaType`.

### Changed

- Caller-supplied attachments still displace the wallet, with one narrowing:
  when they are **all** identity credentials, the wallet's grants are appended
  after them instead. Saying who you are must not stop you saying what you may
  do — under the old rule it did, and the node's denial then read "no grant
  covers this call". Anything else a caller attaches behaves exactly as before.

## [v0.1.6] - 2026-08-10

### Added

- **Verifiable Grants are attached to outbound messages** — automatically, on
  every send path (`Send`, `Request`, and a handler's reply). The cloud-node
  requires a grant for anything its policy does not allow outright, and nothing
  in this SDK attached one: an agent that connected directly sent nothing and
  was denied with "no grant covers this call", a message that reads as "your
  grant is misconfigured" when the truth is "no credential was ever put on the
  wire".

  The wallet reads the holder's credentials from the node, caches them for
  `GrantCacheTTL` (default 60s) and selects the covering set with a mirror of
  the node's authorization policy. Caller-supplied attachments are never
  displaced, and a wallet failure never blocks the send.

  New config: `AttachGrants` (nil means on, env `LAYR8_ATTACH_GRANTS`),
  `GrantCacheTTL`, `GrantReadTimeout`, `OnGrantMiss`. New API:
  `Client.RefreshGrants`, `HeldCredential`, `MaxAttachedGrants`.

- **`OnGrantMiss`** and `GrantMissInfo` — told when the node denied a message
  that went out with nothing attached, when the covering set had to be capped
  at 16, or when the grants could not be read at all. It deliberately stays
  quiet on "nothing covered this message" alone: most traffic (discovery,
  trust-ping, problem reports) needs no grant.

- **MCP over DIDComm** — `Client.MCP()` returns an `*MCPBinding` whose
  `Peer(did)` yields an `*MCPPeer` with `Initialize`, `ListTools` and
  `CallTool`. It handles the protocol subscription, the `tools/call` →
  `{base}/tools-call` type mapping, the JSON-RPC envelope and unwrapping
  `result`. Must be called before `Connect`, like `Handle`. New `*MCPError` for
  a JSON-RPC `error` from the peer; a DIDComm-level failure (including an
  authorization denial) still returns `*ProblemReportError`.

- **`SpaceWatcher`** — the dual-signal poll/diff/notify loop for "does my MCP
  tool surface still look the same", on the semantics every Layr8 SDK shares:
  independent wallet (15s) and resource (60s) intervals, order-independent
  signatures, a first poll that seeds the baseline silently, a fetch error that
  never wipes state, and a two-consecutive-empties debounce on resources but
  never on the wallet.

- **`RESTTimeout`** (default 30s, env `LAYR8_REST_TIMEOUT_MS`) — the REST
  client's deadline is now configurable. It was hard-coded at 30s on
  `http.Client`, which cannot be tightened for a single request, so the grant
  read — which now sits in front of every send — had no way to be bounded more
  tightly than a credential sign. `GrantReadTimeout` (default 2s) is layered on
  with `context`. A negative `RESTTimeout` disables the deadline;
  `GrantReadTimeout` deliberately cannot be disabled, because a zero deadline
  would abort every read before it started and silently attach nothing.

### Changed

- **Every send now performs a credential read against the node before the
  message goes out** (once per `GrantCacheTTL` per DID; failures are cached for
  a shorter window so a misconfigured API key is not a per-message round trip).
  This includes `Send` with `WithFireAndForget()`, which now waits on that read
  before writing. A node that cannot serve `/api/v1/credentials`, or a
  `DialContext` that only routes the WebSocket port, degrades to sending
  unattached — the previous behaviour — and `OnGrantMiss` reports it. Set
  `AttachGrants` to `&false` to opt out entirely.

All exported API is additive; no existing signature or behaviour was removed.

[v0.1.6]: https://github.com/layr8/go-sdk/releases/tag/v0.1.6
