# LAYR8-607: Reply Protocol, Wildcard Binding, Capability Negotiation

## Summary

LAYR8-580 added a new plugin dispatch protocol to the cloud node. The Go SDK needs:

1. **Capability negotiation** - Send `reply_protocol: true` in join params, parse `capabilities` from join reply, operate in new mode (`dispatch_reply`) vs legacy mode
2. **dispatch_reply event** - After each dispatched message, send `dispatch_reply` with status `handled`, `pass`, or `error`
3. **HandleAll** - Wildcard catch-all handler, adds `"*"` to `payload_types`
4. **Remove ack** - Remove `sendAck` from transport interface and public API
5. **ErrPass sentinel** - `var ErrPass = errors.New("pass")` for handler to signal "I don't handle this"

## dispatch_reply status mapping

| Handler result | dispatch_reply status |
|---|---|
| `(nil, nil)` | `handled` |
| `(msg, nil)` | `handled` + send reply message |
| `(nil, ErrPass)` | `pass` |
| `(nil, err)` | `error` with code and message from err |
| no handler + no catch-all | `pass` (auto) |

## dispatch_reply payload (from cloud-node channel.ex)

- `{"status": "handled"}`
- `{"status": "pass"}`
- `{"status": "error", "code": "...", "message": "..."}`

Correlation is implicit — one pending dispatch per channel at a time.
Server stores `pending_dispatch_reply` in socket assigns.

## Capability negotiation (from cloud-node)

- Server returns `capabilities: ["reply_protocol/1", "wildcard/1"]` in join reply
- Server reads `reply_protocol: true` from join params
- Server checks `"*"` in `payload_types` for wildcard

## Dispatch priority

specific handler > catch-all > auto-pass

## Decisions

- Keep `WithManualAck()` and `msg.Ack()` for legacy mode (reply_protocol/1 absent)
- Ack stays in transport interface for legacy mode
- In new mode: no ack, send dispatch_reply instead

## Implementation order (TDD cycles)

1. ErrPass sentinel
2. HandleAll + catch-all in registry (+ `"*"` in payload_types)
3. Capability negotiation (join params + parse reply)
4. dispatch_reply event (new mode handler dispatch)
5. Legacy mode fallback (old server without capabilities)
6. Update existing tests
