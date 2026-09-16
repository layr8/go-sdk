package layr8

// Contract: the DIDComm `trace_context` plaintext header. Parse keeps the
// value, marshal writes it, a malformed value never fails parsing, and a
// handler's reply and handler-error problem report copy the request's value
// unchanged.

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"
)

const testTraceParent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00"

func inboundWithHeader(t *testing.T, header map[string]any) json.RawMessage {
	t.Helper()
	pt := map[string]any{
		"id":   "req-1",
		"type": "https://layr8.io/protocols/echo/1.0/request",
		"from": "did:web:bob",
		"to":   []string{"did:web:alice"},
		"thid": "thread-abc",
		"body": map[string]string{},
	}
	for k, v := range header {
		pt[k] = v
	}
	data, err := json.Marshal(map[string]any{"plaintext": pt})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func wireTraceContext(t *testing.T, data []byte) (json.RawMessage, bool) {
	t.Helper()
	var out map[string]json.RawMessage
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	v, ok := out["trace_context"]
	return v, ok
}

func TestTraceContext_ParseKeepsAndMarshalWrites(t *testing.T) {
	msg, err := parseDIDComm(inboundWithHeader(t, map[string]any{
		"trace_context": map[string]any{"traceparent": testTraceParent, "tracestate": "vendor=value"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := &TraceContext{TraceParent: testTraceParent, TraceState: "vendor=value"}
	if !reflect.DeepEqual(msg.TraceContext, want) {
		t.Fatalf("parsed TraceContext = %+v, want %+v", msg.TraceContext, want)
	}

	data, err := marshalDIDComm(msg)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := wireTraceContext(t, data)
	if !ok {
		t.Fatalf("marshalled message has no trace_context: %s", data)
	}
	if string(raw) != `{"traceparent":"`+testTraceParent+`","tracestate":"vendor=value"}` {
		t.Fatalf("trace_context on the wire = %s", raw)
	}
}

func TestTraceContext_AbsentStaysAbsent(t *testing.T) {
	msg, err := parseDIDComm(inboundWithHeader(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if msg.TraceContext != nil {
		t.Fatalf("TraceContext = %+v, want nil", msg.TraceContext)
	}
	data, _ := marshalDIDComm(msg)
	if _, ok := wireTraceContext(t, data); ok {
		t.Fatalf("absent trace_context was emitted: %s", data)
	}
}

func TestTraceContext_MalformedDoesNotFailParsing(t *testing.T) {
	cases := map[string]any{
		"string":                   "00-abc",
		"null":                     nil,
		"array":                    []string{testTraceParent},
		"number":                   7,
		"object without parent":    map[string]any{"tracestate": "a=b"},
		"non-string traceparent":   map[string]any{"traceparent": 1},
		"object traceparent value": map[string]any{"traceparent": map[string]any{}},
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			msg, err := parseDIDComm(inboundWithHeader(t, map[string]any{"trace_context": value}))
			if err != nil {
				t.Fatalf("parse failed on a malformed trace_context: %v", err)
			}
			if msg.ID != "req-1" {
				t.Fatalf("ID = %q", msg.ID)
			}
			if msg.TraceContext != nil {
				t.Fatalf("TraceContext = %+v, want nil", msg.TraceContext)
			}
		})
	}
}

func TestTraceContext_UnknownMembersAreNotForwarded(t *testing.T) {
	msg, err := parseDIDComm(inboundWithHeader(t, map[string]any{
		"trace_context": map[string]any{"traceparent": testTraceParent, "tracestate": 5, "extra": "x"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := marshalDIDComm(msg)
	raw, _ := wireTraceContext(t, data)
	if string(raw) != `{"traceparent":"`+testTraceParent+`"}` {
		t.Fatalf("trace_context on the wire = %s", raw)
	}
}

// runTraceHandler delivers one request carrying header to a client whose
// handler is h, and returns the outbound messages of type replyType.
func runTraceHandler(t *testing.T, h func(*Message) (*Message, error), header map[string]any, replyType string) []json.RawMessage {
	t.Helper()
	mock, _, wsURL := setupMockServer(t)
	client, _ := NewClient(Config{NodeURL: wsURL, APIKey: "test-key", AgentDID: "did:web:alice"}, discardErrors)
	client.Handle("https://layr8.io/protocols/echo/1.0/request", h)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client.Connect(ctx)
	defer client.Close()

	mock.sendToClient(phoenixMessage{Topic: "plugin:lobby", Event: "message", Payload: inboundWithHeader(t, header)})
	time.Sleep(500 * time.Millisecond)

	var out []json.RawMessage
	for _, m := range mock.getReceived() {
		if m.Event != "message" {
			continue
		}
		var probe struct {
			Type string `json:"type"`
		}
		json.Unmarshal(m.Payload, &probe)
		if probe.Type == replyType {
			out = append(out, m.Payload)
		}
	}
	return out
}

const echoResponseType = "https://layr8.io/protocols/echo/1.0/response"

func echoReply(tc *TraceContext) func(*Message) (*Message, error) {
	return func(*Message) (*Message, error) {
		return &Message{Type: echoResponseType, Body: map[string]string{"echo": "pong"}, TraceContext: tc}, nil
	}
}

var testHeader = map[string]any{
	"trace_context": map[string]any{"traceparent": testTraceParent, "tracestate": "vendor=value"},
}

const testHeaderWire = `{"traceparent":"` + testTraceParent + `","tracestate":"vendor=value"}`

func TestTraceContext_HandlerSeesIt(t *testing.T) {
	seen := make(chan *TraceContext, 1)
	runTraceHandler(t, func(m *Message) (*Message, error) {
		seen <- m.TraceContext
		return nil, nil
	}, testHeader, "none")
	var got *TraceContext
	select {
	case got = <-seen:
	default:
		t.Fatal("handler was not called")
	}
	want := &TraceContext{TraceParent: testTraceParent, TraceState: "vendor=value"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("handler saw %+v, want %+v", got, want)
	}
}

func TestTraceContext_AutoFilledReplyCopiesIt(t *testing.T) {
	out := runTraceHandler(t, echoReply(nil), testHeader, echoResponseType)
	if len(out) != 1 {
		t.Fatalf("got %d replies, want 1", len(out))
	}
	raw, ok := wireTraceContext(t, out[0])
	if !ok || string(raw) != testHeaderWire {
		t.Fatalf("reply trace_context = %s (present=%v), want %s", raw, ok, testHeaderWire)
	}
}

func TestTraceContext_ReplyThatSetsItsOwnKeepsIt(t *testing.T) {
	own := &TraceContext{TraceParent: "00-11111111111111111111111111111111-2222222222222222-00"}
	out := runTraceHandler(t, echoReply(own), testHeader, echoResponseType)
	if len(out) != 1 {
		t.Fatalf("got %d replies, want 1", len(out))
	}
	raw, _ := wireTraceContext(t, out[0])
	if string(raw) != `{"traceparent":"`+own.TraceParent+`"}` {
		t.Fatalf("reply trace_context = %s", raw)
	}
}

func TestTraceContext_HandlerErrorProblemReportCopiesIt(t *testing.T) {
	out := runTraceHandler(t, func(*Message) (*Message, error) {
		return nil, fmt.Errorf("boom")
	}, testHeader, "https://didcomm.org/report-problem/2.0/problem-report")
	if len(out) != 1 {
		t.Fatalf("got %d problem reports, want 1", len(out))
	}
	raw, ok := wireTraceContext(t, out[0])
	if !ok || string(raw) != testHeaderWire {
		t.Fatalf("problem report trace_context = %s (present=%v), want %s", raw, ok, testHeaderWire)
	}
}

func TestTraceContext_NoneOnRequestMeansNoneOnReply(t *testing.T) {
	for name, header := range map[string]map[string]any{
		"absent":    nil,
		"malformed": {"trace_context": "00-not-an-object"},
	} {
		t.Run(name, func(t *testing.T) {
			out := runTraceHandler(t, echoReply(nil), header, echoResponseType)
			if len(out) != 1 {
				t.Fatalf("got %d replies, want 1", len(out))
			}
			if raw, ok := wireTraceContext(t, out[0]); ok {
				t.Fatalf("reply carries trace_context %s, want none", raw)
			}
		})
	}
}
