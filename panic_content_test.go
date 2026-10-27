package layr8

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"
)

// Contract: a handler panic's value never reaches the peer (the problem
// report's comment), the node (dispatch_reply) or a log written by LogErrors.
// The value is whatever the handler was holding, often the message being
// handled. An error the handler RETURNS is its own message to the peer and is
// still sent.

const echoRequestType = "https://layr8.io/protocols/echo/1.0/request"

// runOneHandler connects a client to the mock node, delivers one request whose
// body carries marker, and returns what the client sent and what its
// ErrorHandler received, once the problem report has gone out.
func runOneHandler(t *testing.T, replyProtocol bool, marker string, fn HandlerFunc) ([]phoenixMessage, []SDKError) {
	t.Helper()

	var (
		mock  *mockPhoenixServer
		wsURL string
	)
	if replyProtocol {
		mock, _, wsURL = setupMockServerWithCapabilities(t)
	} else {
		mock, _, wsURL = setupMockServer(t)
	}

	errCh := make(chan SDKError, 4)
	client, err := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	}, func(e SDKError) { errCh <- e })
	if err != nil {
		t.Fatal(err)
	}
	client.Handle(echoRequestType, fn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	inbound, _ := json.Marshal(map[string]interface{}{
		"plaintext": map[string]interface{}{
			"id":   "req-1",
			"type": echoRequestType,
			"from": "did:web:bob",
			"to":   []string{"did:web:alice"},
			"body": map[string]string{"secret": marker},
		},
	})
	mock.sendToClient(phoenixMessage{Topic: "plugins:did:web:alice", Event: "message", Payload: inbound})

	deadline := time.After(3 * time.Second)
	for {
		for _, m := range mock.getReceived() {
			if m.Event == "message" && strings.Contains(string(m.Payload), "problem-report") {
				// Give a dispatch_reply sent right after the report time to land.
				time.Sleep(100 * time.Millisecond)
				var errs []SDKError
				for len(errCh) > 0 {
					errs = append(errs, <-errCh)
				}
				return mock.getReceived(), errs
			}
		}
		select {
		case <-deadline:
			t.Fatal("no problem report was sent")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func problemReportComment(t *testing.T, sent []phoenixMessage) string {
	t.Helper()
	for _, m := range sent {
		if m.Event != "message" {
			continue
		}
		var out struct {
			Type string `json:"type"`
			Body struct {
				Code    string `json:"code"`
				Comment string `json:"comment"`
			} `json:"body"`
		}
		_ = json.Unmarshal(m.Payload, &out)
		if out.Type == "https://didcomm.org/report-problem/2.0/problem-report" {
			if out.Body.Code != "e.p.xfer.cant-process" {
				t.Errorf("problem code = %q, want e.p.xfer.cant-process", out.Body.Code)
			}
			return out.Body.Comment
		}
	}
	t.Fatal("no problem report in what was sent")
	return ""
}

// loggedBy renders errs the way LogErrors writes them.
func loggedBy(errs []SDKError) string {
	var buf bytes.Buffer
	h := LogErrors(log.New(&buf, "", 0))
	for _, e := range errs {
		h(e)
	}
	return buf.String()
}

func TestHandlerPanic_ValueReachesNeitherPeerNodeNorLog(t *testing.T) {
	for _, replyProtocol := range []bool{false, true} {
		t.Run(fmt.Sprintf("replyProtocol=%v", replyProtocol), func(t *testing.T) {
			marker := fmt.Sprintf("SECRET-MARKER-%d", time.Now().UnixNano())

			sent, errs := runOneHandler(t, replyProtocol, marker, func(msg *Message) (*Message, error) {
				var body map[string]string
				_ = msg.UnmarshalBody(&body)
				panic("could not handle " + body["secret"])
			})

			// The path was taken: a panic was reported and answered.
			if len(errs) == 0 || errs[0].Kind != ErrHandlerPanic {
				t.Fatalf("want an ErrHandlerPanic, got %+v", errs)
			}
			if got := problemReportComment(t, sent); got != handlerPanicComment {
				t.Errorf("problem report comment = %q, want the fixed text", got)
			}
			logged := loggedBy(errs)
			if !strings.Contains(logged, "ErrHandlerPanic") {
				t.Errorf("log should name the error kind, got %q", logged)
			}

			for _, m := range sent {
				if strings.Contains(string(m.Payload), marker) {
					t.Errorf("%s sent to the node carries the panic value: %s", m.Event, m.Payload)
				}
			}
			if strings.Contains(logged, marker) {
				t.Errorf("LogErrors output carries the panic value: %s", logged)
			}
		})
	}
}

func TestHandlerReturnedError_IsStillSentToThePeer(t *testing.T) {
	// The control: an error the handler chose to return is its message to the
	// peer and must keep reaching it.
	sent, _ := runOneHandler(t, true, "unused", func(msg *Message) (*Message, error) {
		return nil, errors.New("quota exceeded")
	})
	if got := problemReportComment(t, sent); got != "quota exceeded" {
		t.Errorf("problem report comment = %q, want the handler's returned error", got)
	}
}
