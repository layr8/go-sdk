package layr8

import (
	"fmt"
	"strings"
	"sync"
)

// HandlerFunc is the signature for message handlers.
// Return a response Message to reply, an error to send a problem report,
// or (nil, nil) for fire-and-forget inbound messages.
type HandlerFunc func(msg *Message) (*Message, error)

type handlerEntry struct {
	fn        HandlerFunc
	manualAck bool
}

type handlerRegistry struct {
	mu       sync.RWMutex
	handlers map[string]handlerEntry // message type → handler
	catchAll *handlerEntry           // wildcard catch-all handler
}

func newHandlerRegistry() *handlerRegistry {
	return &handlerRegistry{
		handlers: make(map[string]handlerEntry),
	}
}

func (r *handlerRegistry) register(msgType string, fn HandlerFunc, opts ...HandlerOption) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.handlers[msgType]; exists {
		return fmt.Errorf("handler already registered for message type %q", msgType)
	}

	o := handlerDefaults()
	for _, opt := range opts {
		opt(&o)
	}

	r.handlers[msgType] = handlerEntry{
		fn:        fn,
		manualAck: o.manualAck,
	}
	return nil
}

func (r *handlerRegistry) registerCatchAll(fn HandlerFunc, opts ...HandlerOption) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.catchAll != nil {
		return fmt.Errorf("catch-all handler already registered")
	}

	o := handlerDefaults()
	for _, opt := range opts {
		opt(&o)
	}

	r.catchAll = &handlerEntry{
		fn:        fn,
		manualAck: o.manualAck,
	}
	return nil
}

func (r *handlerRegistry) lookup(msgType string) (handlerEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if entry, ok := r.handlers[msgType]; ok {
		return entry, true
	}
	if r.catchAll != nil {
		return *r.catchAll, true
	}
	return handlerEntry{}, false
}

// payloadTypes returns the payload_types list for the join params.
// Includes unique protocol base URIs derived from registered handlers,
// always includes "report-problem", and appends "*" if a catch-all is registered.
func (r *handlerRegistry) payloadTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[string]struct{})
	types := make([]string, 0)

	// Always register for problem reports — the SDK handles them internally
	// and the cloud node needs to know to route them to connected plugins.
	const problemReportProtocol = "https://didcomm.org/report-problem/2.0"
	seen[problemReportProtocol] = struct{}{}
	types = append(types, problemReportProtocol)

	for msgType := range r.handlers {
		proto := deriveProtocol(msgType)
		if _, ok := seen[proto]; !ok {
			seen[proto] = struct{}{}
			types = append(types, proto)
		}
	}

	if r.catchAll != nil {
		types = append(types, "*")
	}

	return types
}

// deriveProtocol extracts the protocol base URI by removing the last path segment.
// "https://layr8.io/protocols/echo/1.0/request" → "https://layr8.io/protocols/echo/1.0"
func deriveProtocol(msgType string) string {
	idx := strings.LastIndex(msgType, "/")
	if idx == -1 {
		return msgType
	}
	return msgType[:idx]
}
