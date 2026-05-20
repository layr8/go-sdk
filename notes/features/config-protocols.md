# Config.Protocols Feature

## Purpose
Sender-only actors need to declare protocols without registering handlers.
Matches Python SDK's `Config.protocols` pattern.

## Implementation
- `Config.Protocols []string` field added after `Persistent`
- `payloadTypes(extra ...string)` variadic signature merges extras with handler-derived protocols
- Deduplication via `seen` map; problem-report always first
- `client.go` passes `c.cfg.Protocols...` at connect time

## Tests Added
- `TestResolveConfig_ProtocolsPassthrough` - protocols survive resolveConfig
- `TestResolveConfig_NilProtocols` - nil stays nil when not set
- `TestHandlerRegistry_PayloadTypesWithExtraProtocols` - extras merged with handler protocols
- `TestHandlerRegistry_PayloadTypesDeduplicatesExtras` - no duplicates when extra overlaps handler
