package layr8

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// isoLastmod is the shape a Layr8 cloud-node emits today: Elixir's
// DateTime.to_iso8601/1, microsecond precision, Z suffix. The Go SDK typed
// lastmod_time as int64, so a message carrying this failed to decode as a
// whole, and an authorization denial reached its caller as a parse error
// rather than as a denial.
const isoLastmod = "2026-09-10T10:16:00.000000Z"

// isoLastmodSeconds is isoLastmod as UTC epoch seconds.
const isoLastmodSeconds int64 = 1789035360

func attachmentEnvelope(msgType, lastmodJSON string) json.RawMessage {
	lastmod := ""
	if lastmodJSON != "" {
		lastmod = `"lastmod_time":` + lastmodJSON + `,`
	}
	return json.RawMessage(`{
		"context": {"recipient":"did:web:alice","authorized":false,"sender_credentials":[]},
		"plaintext": {
			"id": "msg-lastmod",
			"type": "` + msgType + `",
			"from": "did:web:node",
			"to": ["did:web:alice"],
			"pthid": "thread-1",
			"body": {"code":"e.m.authz.denied","comment":"denied"},
			"attachments": [{
				"id": "helix-decision",
				"media_type": "application/json",
				` + lastmod + `
				"data": {"json": {"reason": "no_grant"}}
			}]
		}
	}`)
}

const denialType = "https://didcomm.org/report-problem/2.0/problem-report"

// TestAttachmentTime_ThreeStatesAreDistinct is the point of the type. An
// absent hint, a hint that was read, and a hint that could not be read are
// three different values. Asserting only two of them passes while the defect
// is present.
func TestAttachmentTime_ThreeStatesAreDistinct(t *testing.T) {
	absent, err := parseDIDComm(attachmentEnvelope(denialType, ""))
	if err != nil {
		t.Fatalf("absent: parseDIDComm() error: %v", err)
	}
	read, err := parseDIDComm(attachmentEnvelope(denialType, `"`+isoLastmod+`"`))
	if err != nil {
		t.Fatalf("read: parseDIDComm() error: %v", err)
	}
	unread, err := parseDIDComm(attachmentEnvelope(denialType, `{"when":"whenever"}`))
	if err != nil {
		t.Fatalf("unread: parseDIDComm() error: %v", err)
	}

	absentTime := absent.Attachments[0].LastmodTime
	readTime := read.Attachments[0].LastmodTime
	unreadTime := unread.Attachments[0].LastmodTime

	if absentTime != nil {
		t.Errorf("absent: LastmodTime = %+v, want nil", absentTime)
	}
	if readTime == nil {
		t.Fatal("read: LastmodTime is nil, want a read value")
	}
	if unreadTime == nil {
		t.Fatal("unread: LastmodTime is nil, want a value marked unread")
	}

	if !readTime.Known {
		t.Error("read: Known = false, want true")
	}
	if unreadTime.Known {
		t.Error("unread: Known = true, want false")
	}
	if len(unreadTime.Raw) == 0 {
		t.Error("unread: Raw is empty, want the JSON that arrived")
	}

	// Pairwise distinct, stated as the three questions a caller asks.
	if _, ok := absentTime.Time(); ok {
		t.Error("absent must not report a time")
	}
	gotTime, ok := readTime.Time()
	if !ok {
		t.Error("read must report a time")
	}
	if _, ok := unreadTime.Time(); ok {
		t.Error("unread must not report a time")
	}
	if gotTime.Unix() != isoLastmodSeconds {
		t.Errorf("read: Time().Unix() = %d, want %d", gotTime.Unix(), isoLastmodSeconds)
	}

	// And "absent" is not the same as "unread": one is nil, the other is a
	// value that says it was not read.
	if (absentTime == nil) == (unreadTime == nil) {
		t.Error("absent and unread must not be represented the same way")
	}
}

func TestAttachmentTime_ReadsIntegerEpoch(t *testing.T) {
	msg, err := parseDIDComm(attachmentEnvelope(denialType, "1789035360"))
	if err != nil {
		t.Fatalf("parseDIDComm() error: %v", err)
	}
	lm := msg.Attachments[0].LastmodTime
	if lm == nil || !lm.Known {
		t.Fatalf("LastmodTime = %+v, want a read value", lm)
	}
	if lm.Seconds != isoLastmodSeconds {
		t.Errorf("Seconds = %d, want %d", lm.Seconds, isoLastmodSeconds)
	}
}

// TestAttachmentTime_StringLastmodReachesTheHandler is the reported symptom,
// end to end: a node sends a message whose attachment carries an ISO-8601
// lastmod_time, and the registered handler must actually run.
func TestAttachmentTime_StringLastmodReachesTheHandler(t *testing.T) {
	mock, _, wsURL := setupMockServer(t)

	const protocol = "https://layr8.io/protocols/echo/1.0/request"
	delivered := make(chan *Message, 1)

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	}, discardErrors)

	client.Handle(protocol, func(msg *Message) (*Message, error) {
		delivered <- msg
		return nil, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client.Connect(ctx)
	defer client.Close()

	mock.sendToClient(phoenixMessage{
		Topic:   "plugins:did:web:alice",
		Event:   "message",
		Payload: attachmentEnvelope(protocol, `"`+isoLastmod+`"`),
	})

	select {
	case msg := <-delivered:
		if len(msg.Attachments) != 1 {
			t.Fatalf("attachments = %d, want 1", len(msg.Attachments))
		}
		lm := msg.Attachments[0].LastmodTime
		if lm == nil || !lm.Known {
			t.Fatalf("LastmodTime = %+v, want a read value", lm)
		}
		if lm.Seconds != isoLastmodSeconds {
			t.Errorf("Seconds = %d, want %d", lm.Seconds, isoLastmodSeconds)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("handler never ran: a message with a string lastmod_time was dropped")
	}
}

// TestAttachmentTime_DenialWithStringLastmodIsReportedAsADenial is the other
// half of the symptom. An orphaned authz denial must reach the caller as
// ErrServerReject carrying the problem report — not as ErrParseFailure, which
// tells the caller nothing about having been denied.
func TestAttachmentTime_DenialWithStringLastmodIsReportedAsADenial(t *testing.T) {
	mock, _, wsURL := setupMockServer(t)

	errCh := make(chan SDKError, 4)
	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	}, func(e SDKError) { errCh <- e })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client.Connect(ctx)
	defer client.Close()

	mock.sendToClient(phoenixMessage{
		Topic:   "plugins:did:web:alice",
		Event:   "message",
		Payload: attachmentEnvelope(denialType, `"`+isoLastmod+`"`),
	})

	select {
	case e := <-errCh:
		if e.Kind == ErrParseFailure {
			t.Fatalf("denial surfaced as a parse failure, not a denial: %v", e.Cause)
		}
		if e.Kind != ErrServerReject {
			t.Fatalf("Kind = %v, want ErrServerReject", e.Kind)
		}
		var prob *ProblemReportError
		if !errors.As(e.Cause, &prob) {
			t.Fatalf("Cause = %v, want a *ProblemReportError", e.Cause)
		}
		if prob.Code != "e.m.authz.denied" {
			t.Errorf("Code = %q, want %q", prob.Code, "e.m.authz.denied")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("denial never reached the caller")
	}
}

// TestParseDIDComm_UnreadableAttachmentsHeaderStillDeliversTheMessage covers
// the general rule behind the lastmod_time fix: attachments are a hint beside
// the message, and a hint this SDK cannot read must not take the message with
// it. "No attachments" and "attachments not read" stay distinct.
func TestParseDIDComm_UnreadableAttachmentsHeaderStillDeliversTheMessage(t *testing.T) {
	broken := json.RawMessage(`{
		"context": {"recipient":"did:web:alice","authorized":false,"sender_credentials":[]},
		"plaintext": {
			"id": "msg-broken",
			"type": "` + denialType + `",
			"from": "did:web:node",
			"to": ["did:web:alice"],
			"body": {"code":"e.m.authz.denied"},
			"attachments": "not-a-list"
		}
	}`)
	msg, err := parseDIDComm(broken)
	if err != nil {
		t.Fatalf("parseDIDComm() error: %v", err)
	}
	if msg.ID != "msg-broken" {
		t.Errorf("ID = %q, want %q", msg.ID, "msg-broken")
	}
	if msg.AttachmentsUnread == nil {
		t.Error("AttachmentsUnread is nil; an undecodable header must say so")
	}
	if msg.Attachments != nil {
		t.Errorf("Attachments = %+v, want nil", msg.Attachments)
	}

	none := json.RawMessage(`{
		"context": {"recipient":"did:web:alice","authorized":false,"sender_credentials":[]},
		"plaintext": {
			"id": "msg-none",
			"type": "` + denialType + `",
			"from": "did:web:node",
			"to": ["did:web:alice"],
			"body": {"code":"e.m.authz.denied"}
		}
	}`)
	quiet, err := parseDIDComm(none)
	if err != nil {
		t.Fatalf("parseDIDComm() error: %v", err)
	}
	if quiet.AttachmentsUnread != nil {
		t.Errorf("AttachmentsUnread = %v, want nil for a message with no attachments", quiet.AttachmentsUnread)
	}
	// Both have nil Attachments, and that is exactly why the second value has
	// to exist: without it the two are indistinguishable.
	if (msg.AttachmentsUnread == nil) == (quiet.AttachmentsUnread == nil) {
		t.Error("an unreadable attachments header and no attachments must not read the same")
	}
}

// TestAttachmentTime_Marshal covers what this SDK puts on the wire: epoch
// seconds for a value it authored or read, and the original bytes for a value
// it could not read, so relaying a peer's message does not rewrite it.
func TestAttachmentTime_Marshal(t *testing.T) {
	authored, err := json.Marshal(NewAttachmentTime(time.Unix(isoLastmodSeconds, 0)))
	if err != nil {
		t.Fatalf("Marshal(authored) error: %v", err)
	}
	if string(authored) != "1789035360" {
		t.Errorf("authored = %s, want 1789035360", authored)
	}

	read := &AttachmentTime{}
	if err := read.UnmarshalJSON([]byte(`"` + isoLastmod + `"`)); err != nil {
		t.Fatalf("UnmarshalJSON(read) error: %v", err)
	}
	out, err := json.Marshal(read)
	if err != nil {
		t.Fatalf("Marshal(read) error: %v", err)
	}
	if string(out) != "1789035360" {
		t.Errorf("read = %s, want 1789035360", out)
	}

	unread := &AttachmentTime{}
	if err := unread.UnmarshalJSON([]byte(`{"when":"whenever"}`)); err != nil {
		t.Fatalf("UnmarshalJSON(unread) error: %v", err)
	}
	out, err = json.Marshal(unread)
	if err != nil {
		t.Fatalf("Marshal(unread) error: %v", err)
	}
	if string(out) != `{"when":"whenever"}` {
		t.Errorf("unread = %s, want the bytes that arrived", out)
	}
}

// TestAttachmentTime_UnmarshalNeverErrors states the guarantee directly: no
// value of any JSON shape may make decoding fail, because decoding failure is
// what made a denial disappear.
func TestAttachmentTime_UnmarshalNeverErrors(t *testing.T) {
	for _, in := range []string{
		`1789035360`, `1789035360.75`, `"` + isoLastmod + `"`, `"1789035360"`,
		`"not a time at all"`, `{}`, `[]`, `true`, `null`, `""`,
	} {
		var a AttachmentTime
		if err := a.UnmarshalJSON([]byte(in)); err != nil {
			t.Errorf("UnmarshalJSON(%s) error = %v, want nil", in, err)
		}
	}
}
