package layr8

// The node pushes a replacement delegated set to a live borrowed child. These
// are the consumer-side boundary tests for that push.
//
// The fake node speaks the wire shape the node's own channel tests assert: a
// join reply whose delegated_credentials carries revision 0, the capability
// ephemeral_delegation_refresh/1, and a push on the child's own topic with
// event delegated_credentials and payload {revision, status, credentials}.
//
// Assertions read the attachments off the WIRE: the wallet is what reaches
// the node, and a push that updated the reading but not the wallet would be
// two answers to one question.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const refreshChild = testParent + ":k7m2q9x4h3bd"

type refreshNode struct {
	*grantNode
	wsURL string

	mu           sync.Mutex
	capabilities []string
	joinReading  string // raw delegated_credentials; "" omits the key
	topic        string
	// pushAfterJoin, when set, is sent as a delegated_credentials push right
	// behind every join reply, from the same handler call: the client's read
	// loop gets the two frames back to back, as it does from a node that
	// re-reads the parent right after the join.
	pushAfterJoin string
}

// stallJoins makes every join on ch wait after it took its reply, so the read
// loop always handles the push behind the reply first — the order a busy
// client sees, made certain rather than likely.
func stallJoins(ch *phoenixChannel) {
	ch.joinReplyReceived = func() { time.Sleep(30 * time.Millisecond) }
}

func setupRefreshNode(t *testing.T) *refreshNode {
	t.Helper()
	gn, wsURL := setupGrantNode(t)
	n := &refreshNode{
		grantNode:    gn,
		wsURL:        wsURL,
		capabilities: []string{"ephemeral_delegation/1", DelegationRefreshCapability},
		joinReading:  readingJSON(t, 0, "complete", "p1"),
	}
	gn.mock.onMsg = func(msg phoenixMessage) {
		if msg.Event == "phx_join" {
			n.mu.Lock()
			n.topic = msg.Topic
			caps, _ := json.Marshal(n.capabilities)
			resp := fmt.Sprintf(`{"capabilities":%s`, caps)
			if n.joinReading != "" {
				resp += `,"delegated_credentials":` + n.joinReading
			}
			resp += "}"
			behind := n.pushAfterJoin
			n.mu.Unlock()
			gn.mock.sendToClient(phoenixMessage{
				JoinRef: msg.Ref, Ref: msg.Ref, Topic: msg.Topic, Event: "phx_reply",
				Payload: json.RawMessage(`{"status":"ok","response":` + resp + `}`),
			})
			if behind != "" {
				gn.mock.sendToClient(phoenixMessage{Topic: msg.Topic, Event: "delegated_credentials", Payload: json.RawMessage(behind)})
			}
			return
		}
		if msg.Ref != "" {
			gn.mock.sendToClient(phoenixMessage{
				Ref: msg.Ref, Topic: msg.Topic, Event: "phx_reply",
				Payload: json.RawMessage(`{"status":"ok","response":{}}`),
			})
		}
	}
	return n
}

// push sends a delegated_credentials event on the joined topic and gives the
// read loop time to apply it.
func (n *refreshNode) push(raw string) {
	n.mu.Lock()
	topic := n.topic
	n.mu.Unlock()
	n.mock.sendToClient(phoenixMessage{Topic: topic, Event: "delegated_credentials", Payload: json.RawMessage(raw)})
	time.Sleep(50 * time.Millisecond)
}

func (n *refreshNode) joinPayloads(t *testing.T) []map[string]json.RawMessage {
	t.Helper()
	var out []map[string]json.RawMessage
	for _, msg := range n.mock.getReceived() {
		if msg.Event != "phx_join" {
			continue
		}
		var p map[string]json.RawMessage
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			t.Fatalf("unmarshal join: %v", err)
		}
		out = append(out, p)
	}
	return out
}

// childFor is the child the node would mint for a parent grant, covering any
// protocol. Its JWT signature carries the parent tag so the wire shows which
// set it came from.
func childFor(t *testing.T, tag string) DelegatedCredential {
	t.Helper()
	c := deliveredGrant(t, "sig"+tag)
	c.ID = "child-of-" + tag
	c.ParentCapability = "urn:uuid:" + tag
	return c
}

func readingJSON(t *testing.T, revision int, status string, tags ...string) string {
	t.Helper()
	creds := []DelegatedCredential{}
	for _, tag := range tags {
		creds = append(creds, childFor(t, tag))
	}
	b, _ := json.Marshal(map[string]any{"revision": revision, "status": status, "credentials": creds})
	return string(b)
}

func borrowedClient(t *testing.T, n *refreshNode) (*Client, context.Context) {
	t.Helper()
	return connectedClient(t, n.wsURL, Config{AgentDID: refreshChild, ParentDID: testParent})
}

// borrowedClientWith connects a borrowed client after letting prepare adjust
// its channel before the first join. It mirrors Client.Connect.
func borrowedClientWith(t *testing.T, n *refreshNode, prepare func(*phoenixChannel)) (*Client, context.Context) {
	t.Helper()
	c, err := NewClient(Config{NodeURL: n.wsURL, APIKey: "test-api-key", AgentDID: refreshChild, ParentDID: testParent}, discardErrors)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_ = c.Handle("https://layr8.io/protocols/echo/1.0/request",
		func(msg *Message) (*Message, error) { return nil, nil })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	c.prepareChannel = prepare
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c, ctx
}

// wireTags sends one message and returns which children rode on it, by tag.
func wireTags(t *testing.T, c *Client, ctx context.Context, n *refreshNode, want map[string]string) []string {
	t.Helper()
	before := 0
	for _, m := range n.mock.getReceived() {
		if m.Event == "message" {
			before++
		}
	}
	if err := c.Send(ctx, &Message{Type: "https://layr8.io/protocols/echo/1.0/ping", To: []string{"did:web:peer"}}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		seen := 0
		for _, m := range n.mock.getReceived() {
			if m.Event != "message" {
				continue
			}
			seen++
			if seen <= before {
				continue
			}
			var env struct {
				Attachments []struct {
					Data struct {
						JWS string `json:"jws"`
					} `json:"data"`
				} `json:"attachments"`
			}
			_ = json.Unmarshal(m.Payload, &env)
			var tags []string
			for _, a := range env.Attachments {
				tags = append(tags, want[a.Data.JWS])
			}
			sort.Strings(tags)
			return tags
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the node never received the message")
	return nil
}

// jwsIndex maps each tag's JWT back to the tag.
func jwsIndex(t *testing.T, tags ...string) map[string]string {
	m := map[string]string{}
	for _, tag := range tags {
		m[childFor(t, tag).CredentialJWT] = tag
	}
	return m
}

func credIDs(r *DelegatedCredentialsReading) []string {
	if r == nil {
		return nil
	}
	ids := []string{}
	for _, c := range r.Credentials {
		ids = append(ids, c.ID)
	}
	return ids
}

func TestRefreshIsRequestedOnlyByAJoinThatNamesAParent(t *testing.T) {
	n := setupRefreshNode(t)
	borrowedClient(t, n)
	if got := string(n.joinPayloads(t)[0]["delegation_refresh"]); got != "true" {
		t.Errorf("delegation_refresh = %q, want true", got)
	}

	plain := setupRefreshNode(t)
	connectedClient(t, plain.wsURL, Config{})
	if _, present := plain.joinPayloads(t)[0]["delegation_refresh"]; present {
		t.Error("an unparented join carries delegation_refresh")
	}
}

func TestTheRefreshCapabilityIsReportedAndItsAbsenceToo(t *testing.T) {
	n := setupRefreshNode(t)
	c, _ := borrowedClient(t, n)
	if !c.SupportsEphemeralDelegationRefresh() {
		t.Error("ephemeral_delegation_refresh/1 was announced and not recorded")
	}

	old := setupRefreshNode(t)
	old.capabilities = []string{"ephemeral_delegation/1"}
	oc, _ := borrowedClient(t, old)
	if oc.SupportsEphemeralDelegationRefresh() {
		t.Error("refresh reported supported by a node that did not announce it")
	}
	if !oc.SupportsEphemeralDelegation() {
		t.Error("ephemeral_delegation/1 lost")
	}
}

func TestAPushReplacesTheReadingTheWireAndFiresOnDelegation(t *testing.T) {
	n := setupRefreshNode(t)
	idx := jwsIndex(t, "p1", "p2")

	var mu sync.Mutex
	var events []string
	cfg := Config{AgentDID: refreshChild, ParentDID: testParent}
	cfg.NodeURL = n.wsURL
	cfg.APIKey = "test-api-key"
	c, err := NewClient(cfg, discardErrors)
	if err != nil {
		t.Fatal(err)
	}
	c.OnDelegation(func(did string, r *DelegatedCredentialsReading) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, fmt.Sprintf("%s %s %v", did, r.Status, credIDs(r)))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if got := wireTags(t, c, ctx, n, idx); fmt.Sprint(got) != "[p1]" {
		t.Fatalf("before the push the wire carried %v, want [p1]", got)
	}

	n.push(readingJSON(t, 1, "complete", "p2"))

	if got := credIDs(c.DelegatedCredentials()); fmt.Sprint(got) != "[child-of-p2]" {
		t.Errorf("DelegatedCredentials = %v, want [child-of-p2]", got)
	}
	// Replaced, never appended.
	if got := wireTags(t, c, ctx, n, idx); fmt.Sprint(got) != "[p2]" {
		t.Errorf("after the push the wire carried %v, want [p2]", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := fmt.Sprintf("[%s complete [child-of-p2]]", refreshChild); fmt.Sprint(events) != want {
		t.Errorf("OnDelegation calls = %v, want %v", events, want)
	}
}

func TestACompleteEmptyPushIsAMeasurementAndIsApplied(t *testing.T) {
	n := setupRefreshNode(t)
	c, ctx := borrowedClient(t, n)
	n.push(readingJSON(t, 1, "complete"))

	r := c.DelegatedCredentials()
	if r == nil || r.Status != DelegationComplete || len(r.Credentials) != 0 {
		t.Fatalf("DelegatedCredentials = %+v, want complete with none", r)
	}
	if got := wireTags(t, c, ctx, n, jwsIndex(t, "p1")); len(got) != 0 {
		t.Errorf("the wire still carried %v", got)
	}
}

func TestAPushCorrectsAJoinWhoseReadingWasUnread(t *testing.T) {
	n := setupRefreshNode(t)
	n.joinReading = `{"revision":0,"status":"unread","credentials":[]}`
	c, ctx := borrowedClient(t, n)
	n.push(readingJSON(t, 1, "partial", "p3"))

	r := c.DelegatedCredentials()
	if r == nil || r.Status != DelegationPartial {
		t.Fatalf("DelegatedCredentials = %+v, want partial", r)
	}
	if got := wireTags(t, c, ctx, n, jwsIndex(t, "p3")); fmt.Sprint(got) != "[p3]" {
		t.Errorf("the wire carried %v, want [p3]", got)
	}
}

func TestAStaleRevisionIsIgnored(t *testing.T) {
	n := setupRefreshNode(t)
	c, ctx := borrowedClient(t, n)
	n.push(readingJSON(t, 2, "complete", "p2"))

	var calls atomic.Int32
	c.OnDelegation(func(string, *DelegatedCredentialsReading) { calls.Add(1) })

	n.push(readingJSON(t, 1, "complete", "stale")) // late
	n.push(readingJSON(t, 2, "complete", "dup"))   // repeated

	if got := credIDs(c.DelegatedCredentials()); fmt.Sprint(got) != "[child-of-p2]" {
		t.Errorf("DelegatedCredentials = %v, want [child-of-p2]", got)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("OnDelegation fired %d times for stale pushes", n)
	}
	if got := wireTags(t, c, ctx, n, jwsIndex(t, "p2", "stale", "dup")); fmt.Sprint(got) != "[p2]" {
		t.Errorf("the wire carried %v, want [p2]", got)
	}
}

func TestAPushThatIsNotAReadingIsIgnored(t *testing.T) {
	n := setupRefreshNode(t)
	c, ctx := borrowedClient(t, n)

	for _, bad := range []string{
		`null`,
		`[]`,
		`{"revision":1,"credentials":[]}`,
		`{"revision":1,"status":"bogus","credentials":[]}`,
		`{"revision":1,"status":"complete"}`,
		`{"status":"complete","credentials":[]}`,
		`{"revision":"1","status":"complete","credentials":[]}`,
		`{"revision":-1,"status":"complete","credentials":[]}`,
		`{"revision":1.5,"status":"complete","credentials":[]}`,
		// Never sent by the node: a failed read pushes nothing.
		`{"revision":1,"status":"unread","credentials":[]}`,
	} {
		n.push(bad)
	}

	if got := credIDs(c.DelegatedCredentials()); fmt.Sprint(got) != "[child-of-p1]" {
		t.Errorf("DelegatedCredentials = %v, want the join reading [child-of-p1]", got)
	}
	if got := wireTags(t, c, ctx, n, jwsIndex(t, "p1")); fmt.Sprint(got) != "[p1]" {
		t.Errorf("the wire carried %v, want [p1]", got)
	}
}

func TestAPushOnAJoinThatDidNotOptInIsIgnored(t *testing.T) {
	n := setupRefreshNode(t)
	c, _ := connectedClient(t, n.wsURL, Config{})
	before := credIDs(c.DelegatedCredentials())
	if fmt.Sprint(before) != "[child-of-p1]" {
		t.Fatalf("join reading = %v", before)
	}
	n.push(readingJSON(t, 1, "complete"))
	if got := credIDs(c.DelegatedCredentials()); fmt.Sprint(got) != "[child-of-p1]" {
		t.Errorf("DelegatedCredentials = %v after a push nobody asked for", got)
	}
}

func TestNoPushMeansTheJoinReadingStillStands(t *testing.T) {
	n := setupRefreshNode(t)
	c, ctx := borrowedClient(t, n)
	time.Sleep(100 * time.Millisecond)
	if got := wireTags(t, c, ctx, n, jwsIndex(t, "p1")); fmt.Sprint(got) != "[p1]" {
		t.Errorf("the wire carried %v, want [p1]", got)
	}
}

func TestAPanickingOnDelegationIsReportedAndTheSocketKeepsReading(t *testing.T) {
	n := setupRefreshNode(t)
	var mu sync.Mutex
	var errs []SDKError
	cfg := Config{AgentDID: refreshChild, ParentDID: testParent, NodeURL: n.wsURL, APIKey: "test-api-key"}
	c, err := NewClient(cfg, func(e SDKError) { mu.Lock(); errs = append(errs, e); mu.Unlock() })
	if err != nil {
		t.Fatal(err)
	}
	c.OnDelegation(func(string, *DelegatedCredentialsReading) { panic("listener broke") })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	n.push(readingJSON(t, 1, "complete", "p2"))
	n.push(readingJSON(t, 2, "complete", "p3"))

	mu.Lock()
	defer mu.Unlock()
	if len(errs) != 2 || errs[0].Kind != ErrHandlerPanic {
		t.Errorf("errors = %+v, want two ErrHandlerPanic", errs)
	}
	if got := credIDs(c.DelegatedCredentials()); fmt.Sprint(got) != "[child-of-p3]" {
		t.Errorf("DelegatedCredentials = %v, want [child-of-p3]", got)
	}
}

// TestARejoinStartsTheRevisionAgain pushes only after the rejoin has settled;
// the push that arrives right behind a rejoin reply is covered by
// TestAPushRightBehindARejoinReply*.
func TestARejoinStartsTheRevisionAgain(t *testing.T) {
	n := setupRefreshNode(t)
	c, ctx := borrowedClient(t, n)
	n.push(readingJSON(t, 3, "complete", "p3"))

	ch := c.transport.(*phoenixChannel)
	if err := ch.join(ctx, ch.protocols); err != nil {
		t.Fatalf("rejoin: %v", err)
	}
	if got := credIDs(c.DelegatedCredentials()); fmt.Sprint(got) != "[child-of-p1]" {
		t.Fatalf("after rejoin DelegatedCredentials = %v, want the join reading", got)
	}

	n.push(readingJSON(t, 1, "complete", "p4"))
	if got := credIDs(c.DelegatedCredentials()); fmt.Sprint(got) != "[child-of-p4]" {
		t.Errorf("DelegatedCredentials = %v, want [child-of-p4]", got)
	}
}

// TestASendRacingAPushUsesOneSetNeverAMix parks a send on its credential read,
// applies a push, and releases the read. The message must carry the whole old
// set; the next one the whole new set.
func TestASendRacingAPushUsesOneSetNeverAMix(t *testing.T) {
	n := setupRefreshNode(t)
	n.joinReading = readingJSON(t, 0, "complete", "old-a", "old-b")
	block := make(chan struct{})
	n.grantNode.mu.Lock()
	n.grantNode.block = block
	n.grantNode.mu.Unlock()

	cfg := Config{AgentDID: refreshChild, ParentDID: testParent, GrantReadTimeout: 5 * time.Second}
	c, ctx := connectedClient(t, n.wsURL, cfg)
	idx := jwsIndex(t, "old-a", "old-b", "new-a", "new-b")

	done := make(chan []string, 1)
	go func() { done <- wireTags(t, c, ctx, n, idx) }()
	time.Sleep(100 * time.Millisecond)
	n.push(readingJSON(t, 1, "complete", "new-a", "new-b"))

	n.grantNode.mu.Lock()
	n.grantNode.block = nil
	n.grantNode.mu.Unlock()
	close(block)

	if got := <-done; fmt.Sprint(got) != "[old-a old-b]" {
		t.Errorf("the racing send carried %v, want [old-a old-b]", got)
	}
	if got := wireTags(t, c, ctx, n, idx); fmt.Sprint(got) != "[new-a new-b]" {
		t.Errorf("the next send carried %v, want [new-a new-b]", got)
	}
}

func TestPushReadingsArePairwiseDistinct(t *testing.T) {
	type parsed struct {
		r   *DelegatedCredentialsReading
		rev int64
		ok  bool
	}
	p := func(raw string) parsed {
		r, rev, ok := parseDelegationPush(json.RawMessage(raw))
		return parsed{r, rev, ok}
	}
	completeEmpty := p(`{"revision":1,"status":"complete","credentials":[]}`)
	completeSome := p(`{"revision":1,"status":"complete","credentials":[{"id":"a","parent_capability":"b","credential_jwt":"c"}]}`)
	partial := p(`{"revision":1,"status":"partial","credentials":[{"id":"a","parent_capability":"b","credential_jwt":"c"}]}`)
	unread := p(`{"revision":1,"status":"unread","credentials":[]}`)

	if !completeEmpty.ok || completeEmpty.r.Status != DelegationComplete || len(completeEmpty.r.Credentials) != 0 {
		t.Errorf("complete-empty = %+v", completeEmpty)
	}
	if !completeSome.ok || len(completeSome.r.Credentials) != 1 {
		t.Errorf("complete-some = %+v", completeSome)
	}
	if !partial.ok || partial.r.Status != DelegationPartial {
		t.Errorf("partial = %+v", partial)
	}
	if unread.ok {
		t.Error("an unread push parsed as a reading to apply")
	}
}

func (n *refreshNode) setPushAfterJoin(raw string) {
	n.mu.Lock()
	n.pushAfterJoin = raw
	n.mu.Unlock()
}

// assertReadingAndWire checks the accessor and the attachments on the wire
// agree on one tag.
func assertReadingAndWire(t *testing.T, c *Client, ctx context.Context, n *refreshNode, tag string, all ...string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	want := "[child-of-" + tag + "]"
	for fmt.Sprint(credIDs(c.DelegatedCredentials())) != want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := credIDs(c.DelegatedCredentials()); fmt.Sprint(got) != want {
		t.Fatalf("DelegatedCredentials = %v, want %s", got, want)
	}
	if got := wireTags(t, c, ctx, n, jwsIndex(t, all...)); fmt.Sprint(got) != "["+tag+"]" {
		t.Fatalf("wire = %v, want [%s]", got, tag)
	}
}

// A push right behind the join reply is applied on a first join, not dropped
// because the join had not yet recorded that it opted in.
func TestAPushRightBehindTheJoinReplyIsApplied(t *testing.T) {
	n := setupRefreshNode(t)
	n.setPushAfterJoin(readingJSON(t, 1, "complete", "p9"))
	c, ctx := borrowedClientWith(t, n, stallJoins)
	assertReadingAndWire(t, c, ctx, n, "p9", "p1", "p9")
}

// On a rejoin after revision 0, a push right behind the reply is not
// overwritten by the (older) join reading.
func TestAPushRightBehindARejoinReplyIsNotOverwritten(t *testing.T) {
	n := setupRefreshNode(t)
	c, ctx := borrowedClient(t, n)
	n.setPushAfterJoin(readingJSON(t, 1, "complete", "p9"))
	ch := c.transport.(*phoenixChannel)
	stallJoins(ch)
	if err := ch.join(ctx, ch.protocols); err != nil {
		t.Fatalf("rejoin: %v", err)
	}
	assertReadingAndWire(t, c, ctx, n, "p9", "p1", "p9")
}

// On a rejoin after revision 3, the new connection's revision 1 right behind
// the reply is not compared with the old connection's revision.
func TestAPushRightBehindARejoinReplyIsNotComparedWithTheOldRevision(t *testing.T) {
	n := setupRefreshNode(t)
	c, ctx := borrowedClient(t, n)
	n.push(readingJSON(t, 3, "complete", "p3"))
	n.setPushAfterJoin(readingJSON(t, 1, "complete", "p9"))
	ch := c.transport.(*phoenixChannel)
	stallJoins(ch)
	if err := ch.join(ctx, ch.protocols); err != nil {
		t.Fatalf("rejoin: %v", err)
	}
	assertReadingAndWire(t, c, ctx, n, "p9", "p1", "p3", "p9")
}

// A push that lands while a join is handing its reading to the wallet must
// not be overwritten there: the reading and the wallet end on the same set.
func TestAPushDuringTheJoinsWalletSeedDoesNotLoseToIt(t *testing.T) {
	n := setupRefreshNode(t)
	c, ctx := borrowedClient(t, n)
	ch := c.transport.(*phoenixChannel)

	orig := ch.delegatedFn
	var first atomic.Bool
	pushed := make(chan struct{})
	ch.delegatedFn = func(did string, reading *DelegatedCredentialsReading) {
		// Not sync.Once: Once would make the push's own call wait here.
		if first.CompareAndSwap(false, true) {
			// The push is handled by another goroutine, as the read loop
			// would, while this join has updated its state but not yet
			// seeded the wallet.
			go func() {
				defer close(pushed)
				ch.handleInbound(phoenixMessage{Topic: ch.topic, Event: "delegated_credentials",
					Payload: json.RawMessage(readingJSON(t, 1, "complete", "p9"))})
			}()
			time.Sleep(50 * time.Millisecond)
		}
		orig(did, reading)
	}
	if err := ch.join(ctx, ch.protocols); err != nil {
		t.Fatalf("rejoin: %v", err)
	}
	<-pushed
	assertReadingAndWire(t, c, ctx, n, "p9", "p1", "p9")
}

// A join that fails discards what it held: a later join does not replay it.
func TestAFailedJoinDiscardsHeldPushes(t *testing.T) {
	n := setupRefreshNode(t)
	c, ctx := borrowedClient(t, n)
	ch := c.transport.(*phoenixChannel)

	ch.mu.Lock()
	ch.joining = true
	ch.heldPushes = nil
	ch.mu.Unlock()
	ch.handleInbound(phoenixMessage{Topic: ch.topic, Event: "delegated_credentials",
		Payload: json.RawMessage(readingJSON(t, 7, "complete", "p7"))})

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := ch.join(cctx, ch.protocols); err == nil {
		t.Fatal("join with a cancelled context succeeded")
	}
	ch.mu.Lock()
	joining, held := ch.joining, len(ch.heldPushes)
	ch.mu.Unlock()
	if joining || held != 0 {
		t.Fatalf("after a failed join: joining=%v held=%d, want false 0", joining, held)
	}
	// Let the cancelled join's reply arrive and be ignored, then rejoin.
	time.Sleep(50 * time.Millisecond)
	if err := ch.join(ctx, ch.protocols); err != nil {
		t.Fatalf("rejoin: %v", err)
	}
	assertReadingAndWire(t, c, ctx, n, "p1", "p1", "p7")
}
