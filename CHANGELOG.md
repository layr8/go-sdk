# Changelog

All notable changes to `github.com/layr8/go-sdk`. Format loosely follows [Keep a Changelog](https://keepachangelog.com/); versioning follows [SemVer](https://semver.org/).

This file starts here. Earlier releases are recorded only in git history.

## [Unreleased]

### Added

- **A borrowed child's delegated set is kept current while it is connected.**
  A join that names a `ParentDID` now sends `delegation_refresh: true`. When
  the node announces `ephemeral_delegation_refresh/1`, it pushes a
  `delegated_credentials` event with the whole new set whenever the parent's
  grants change. The client replaces the reading and the attached credentials,
  ignores a push whose `revision` is not newer than the one it holds, ignores a
  push that does not parse (and an `unread` push, which the node never sends),
  and calls `OnDelegation(did, reading)`. A rejoin starts the revision again
  from the join reply. A push that arrives while a join is waiting for or
  installing its reply is held until the join has handed its reading to the
  wallet, then applied in order, so it is neither dropped nor overwritten by
  the older join reading.
- `Client.SupportsEphemeralDelegationRefresh()`, `Client.OnDelegation()` and
  `DelegationRefreshCapability`.

### Fixed

- The delegation reading and capability flags are now read and written under a
  lock; a push arrives on the read goroutine.

## [v0.2.2] - 2026-09-16

### Added

- **The `trace_context` plaintext header is carried.** A DIDComm message may
  carry a W3C trace context in a top-level `trace_context` object
  (`traceparent`, optional `tracestate`). The SDK used to drop it on parse and
  never wrote it. It is now `Message.TraceContext` (type `*TraceContext`):
  parsing reads it, marshalling writes it, and `Send` / `Request` carry a value
  the caller sets.
- **A handler's reply joins the request's trace.** `autoFillResponse` copies
  the request's `TraceContext` unchanged unless the handler set its own, next
  to where it already defaults `ThreadID`. The problem report sent for a
  failed handler copies it too.

  A value that is not an object with a string `traceparent` is dropped, never
  a parse error, and members other than `traceparent` and `tracestate` are not
  forwarded. The SDK does not validate the `traceparent` format. It does not
  yet create a trace context for a new request that has none. The node SDK
  makes the same change.

## [v0.2.1] - 2026-09-15

### Fixed

- **`SignCredential` fills in `ID` and `Issuer` when the caller leaves them
  empty.** The cloud-node requires both fields on the credential and answers
  `422 Invalid credential: missing required fields` when either is absent,
  without saying which. `Credential` marks both `omitempty`, so a credential
  built with only `CredentialSubject` was always rejected. Before sending, an
  empty `Issuer` is now set to the DID the credential is signed with
  (`WithIssuerDID`, else the agent DID) and an empty `ID` to a fresh
  `urn:uuid:<UUID v4>`. A value the caller sets is sent unchanged; a
  caller-supplied `Issuer` is not compared with the signing DID. The node,
  Python and Elixir SDKs get the same change.

## [v0.2.0] - 2026-09-10

### Added

- **A join can name the parent whose authority its DID borrows, and this SDK
  derives the name.** `Config.ParentDID` is optional and is sent only when set,
  so a join that names no parent puts exactly the payload on the wire it did
  before — asserted byte for byte in `borrowed_did_test.go`. Pass `ParentDID`
  and leave `AgentDID` empty, and the client joins as `<ParentDID>:<segment>`:
  twelve characters of Crockford base32 from a cryptographic source, generated
  once when the configuration is resolved, so a reconnect returns under the same
  DID and the node re-mints the same credentials for it.

  **The reason the shape is fixed:** a cloud-node API key restricts which DIDs
  it may bind, and an entry is either an exact DID or a prefix with a trailing
  `*`. While a borrower's name was unrelated to its parent — and generated per
  connection — no entry could be written for it in advance, so the only key that
  admitted a borrower was one with *no restrictions at all*, which admits every
  DID on the node. Named beneath its parent, one key carrying the parent and
  `DIDNamespaceOf(parent)` admits the parent and its borrowers and nothing else.

  A caller that supplies its own `AgentDID` that is **not** named beneath the
  parent gets a `*NotBeneathParentError` from `NewClient`, before anything is
  written: the node refuses that join with
  `e.join.plugin.child.not-beneath-parent`, and a refusal at connect time in
  production is the expensive way to learn this.

  New exports: `ParentDID`, `DIDNamespaceOf`, `IsBeneathParent`,
  `RandomChildSegment`, `ChildSegmentLength`, `ChildNameSource`,
  `NotBeneathParentError`.

  `did_spec.childNameSource` is sent alongside `parentDid` — `"sdk"` when this
  library generated the segment, `"client"` when the caller supplied the whole
  DID, and the key is **absent** when neither applies. A generated name and a
  hand-built one that conforms are otherwise identical bytes, so without it a
  malformed borrower DID could not be told apart as this library's defect from a
  caller's typo. The absent case is never folded into `"client"`.

  Only a temporary identity may borrow: the node refuses a join that names a
  parent and declares persistent storage with
  `e.join.plugin.child.storage-not-ephemeral`, so `ParentDID` and
  `Persistent: true` do not go together. This SDK already defaults to ephemeral
  storage, so naming a parent needs no other change.

- **The join reply carries the credentials the node signed for this DID.**
  `Client.DelegatedCredentials()` returns a `*DelegatedCredentialsReading` —
  `{Status, Credentials}` — with one entry per grant the named parent holds. The
  node signs them at join, narrowed to no more than the parent carries and
  citing it in `credentialSubject.delegation.parentCapability`. When
  `AttachGrants` is on they are attached to outbound messages automatically;
  there is nothing to wire up.

  **Four readings from this method, and six with
  `Client.SupportsEphemeralDelegation()`. Collapsing any pair reports something
  nobody measured.**

  | `DelegatedCredentials()` | `SupportsEphemeralDelegation()` | Meaning |
  |---|---|---|
  | `nil` | `true` | the join named no parent |
  | `{complete, []}` | `true` | the parent's wallet was **read** and it grants nothing |
  | `{complete, [...]}` | `true` | read, and here is all of it |
  | `{partial, [...]}` | `true` | read, and some of it could not be delegated — there is more you did not get |
  | `{unread, []}` | `true` | the wallet could **not** be read; the `[]` measures nothing |
  | `nil` | `false` | the node never looked |

  Anything that is not a well-formed reading — absent, a bare array from an
  older node, an unknown status — is `nil`, never an empty `complete` one: that
  would state that a wallet was read and grants nothing, which is the one thing
  none of those inputs says.

  A reading arrives on **every** join and rejoin, including one that carries no
  reading at all — that clears whatever the previous join seeded, because the
  node mints a fresh set per join and the previous set names a DID document a
  rejoin may have replaced.

  **The credential exists nowhere but the join reply.** The node stores nothing
  about it, so `GET /api/v1/credentials` will never return it and no endpoint
  will hand it back; rejoin to be issued a new one. It is not individually
  revocable — authority is withdrawn by revoking or expiring the parent's grant.
  Because that endpoint is not their source, a failed read of it no longer
  withholds them from a message that they cover.

### Changed

- **Breaking:** `Attachment.LastmodTime` is now `*AttachmentTime` instead of
  `int64`. DIDComm v2 states no type for `lastmod_time` — only "a hint about
  when the content in this attachment was last modified" — while pinning
  `created_time`/`expires_time` to integer UTC epoch seconds in the same
  document. Integers and RFC 3339 strings are both read; anything else is
  recorded as unread rather than rejected. The three cases are three values:
  a nil pointer (absent), `Known == true` with `Seconds` (read), and
  `Known == false` with `Raw` (not read). Callers that read the old `int64`
  should call `Time()` or check `Known`; callers that set it should use
  `NewAttachmentTime(t)`.
- Outbound `lastmod_time` is emitted as an integer, which is what both DIF
  reference implementations (`didcomm-rust`, `didcomm-python`) expect — a value
  that arrived as an RFC 3339 string and was read is normalized to seconds on
  the way out. Only a value this SDK could **not** read is re-emitted byte for
  byte, so relaying a message never rewrites a field nobody decoded.

### Fixed

- **An attachment field this SDK could not read no longer swallows the whole
  message.** A cloud-node's `e.m.authz.denied` problem report carries a
  `helix-decision` attachment whose `lastmod_time` is an ISO-8601 string; this
  SDK typed the field as `int64`, so decoding failed for the entire message and
  the caller was handed `ErrParseFailure` instead of the denial. Being denied
  and hearing nothing are not the same event, and the SDK reported the wrong
  one.
- `attachments` are now decoded in a second pass. A header this SDK cannot read
  leaves `Message.Attachments` nil and sets the new `Message.AttachmentsUnread`
  to say so, and the message is still delivered. "No attachments" and
  "attachments not read" are different facts and now have different values.

## [v0.1.7] - 2026-08-21

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

[v0.2.2]: https://github.com/layr8/go-sdk/releases/tag/v0.2.2
[v0.2.1]: https://github.com/layr8/go-sdk/releases/tag/v0.2.1
[v0.2.0]: https://github.com/layr8/go-sdk/releases/tag/v0.2.0
[v0.1.7]: https://github.com/layr8/go-sdk/releases/tag/v0.1.7
[v0.1.6]: https://github.com/layr8/go-sdk/releases/tag/v0.1.6
