# DIDComm v2 Attachment Support

## Summary
Add attachment support to the Go SDK to match Node and Elixir SDK patterns.

## Key decisions
- Attachment and AttachmentData types in message.go (not a new file) to stay consistent with existing layout
- Attachments field on Message uses `json:"-"` (handled in marshal/parse, not direct JSON)
- didcommEnvelope gets `Attachments []Attachment` with `json:"attachments,omitempty"`
- marshalDIDComm and parseDIDComm updated to pass through attachments

## Status
- [x] RED: Write failing tests
- [x] GREEN: Implement
- [x] REFACTOR: Clean up
