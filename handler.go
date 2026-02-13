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

func (r *handlerRegistry) lookup(msgType string) (handlerEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.handlers[msgType]
	return entry, ok
}

// protocols returns the unique protocol base URIs derived from registered handler message types.
// For example, "https://layr8.io/protocols/echo/1.0/request" derives "https://layr8.io/protocols/echo/1.0".
func (r *handlerRegistry) protocols() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[string]struct{})
	var protocols []string

	for msgType := range r.handlers {
		proto := deriveProtocol(msgType)
		if _, ok := seen[proto]; !ok {
			seen[proto] = struct{}{}
			protocols = append(protocols, proto)
		}
	}
	return protocols
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
