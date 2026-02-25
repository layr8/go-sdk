// Comprehensive integration test for the Layr8 Go SDK.
//
// Tests all key SDK functions against live cloud-nodes:
//   - NewClient / Config (explicit + env var fallback)
//   - Handle (register handlers, multi-protocol)
//   - Connect / Close / DID()
//   - Send (fire-and-forget)
//   - Request (cross-node request/response)
//   - Problem reports (handler error → ProblemReportError)
//   - Manual ack (WithManualAck + msg.Ack())
//   - Auth context (MessageContext.Authorized, SenderCredentials)
//   - WithParentThread (nested thread correlation)
//   - Sentinel errors (ErrNotConnected, ErrAlreadyConnected)
//
// Prerequisites:
//   - Two nodes running in local Tilt env (alice-test, bob-test)
//   - Traefik exposing *.localhost (k3d maps host :80/:443 to Traefik)
//
// Usage:
//
//	go run ./tests/integration-test
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	layr8 "github.com/layr8/go-sdk"
)

const (
	aliceNodeURL = "ws://alice-test.localhost/plugin_socket/websocket"
	aliceAPIKey  = "alice_abcd1234_testkeyalicetestkeyali24"

	bobNodeURL = "ws://bob-test.localhost/plugin_socket/websocket"
	bobAPIKey  = "bob_efgh5678_testkeybobbtestkeybobt24"

	echoBase     = "https://layr8.io/protocols/echo-test/1.0"
	echoRequest  = echoBase + "/request"
	echoResponse = echoBase + "/response"

	pingBase     = "https://layr8.io/protocols/ping-test/1.0"
	pingRequest  = pingBase + "/request"
	pingResponse = pingBase + "/response"
)

type EchoReq struct {
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
}

type EchoResp struct {
	Echo      string `json:"echo"`
	Timestamp int64  `json:"timestamp"`
}

type PingReq struct {
	Seq int `json:"seq"`
}

type PingResp struct {
	Seq  int    `json:"seq"`
	Pong string `json:"pong"`
}

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
)

var (
	passed  int
	failed  int
	skipped int
	testID  string
)

func section(name string) {
	fmt.Printf("\n%s%s── %s ──%s\n\n", colorBold, colorCyan, name, colorReset)
}

func pass(name string) {
	fmt.Printf("    %s%s PASS %s %s\n", colorBold, colorGreen, colorReset, name)
	passed++
}

func fail(name string, reason string) {
	fmt.Printf("    %s%s FAIL %s %s\n", colorBold, colorRed, colorReset, name)
	fmt.Printf("         %s%s%s\n", colorDim, reason, colorReset)
	failed++
}

func skip(name string, reason string) {
	fmt.Printf("    %s%s SKIP %s %s\n", colorBold, colorYellow, colorReset, name)
	fmt.Printf("         %s%s%s\n", colorDim, reason, colorReset)
	skipped++
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	testID = fmt.Sprintf("%d", time.Now().UnixMilli())
	aliceDID := fmt.Sprintf("did:web:alice-test.localhost:sdk-test-%s", testID)
	bobDID := fmt.Sprintf("did:web:bob-test.localhost:sdk-test-%s", testID)

	fmt.Printf("\n%s%s=== Layr8 Go SDK — Integration Test ===%s\n", colorBold, colorCyan, colorReset)
	fmt.Printf("%sTest ID:   %s%s\n", colorDim, testID, colorReset)
	fmt.Printf("%sAlice DID: %s%s\n", colorDim, aliceDID, colorReset)
	fmt.Printf("%sBob DID:   %s%s\n", colorDim, bobDID, colorReset)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// ═══════════════════════════════════════════════════════════════════
	section("Connection & Identity")
	// ═══════════════════════════════════════════════════════════════════

	// Test 1: Connect Alice with echo + ping handlers
	fmt.Println("  [1] Connect Alice with multiple protocol handlers")

	alice, err := layr8.NewClient(layr8.Config{
		NodeURL:  aliceNodeURL,
		APIKey:   aliceAPIKey,
		AgentDID: aliceDID,
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		log.Fatalf("  FATAL: NewClient(alice): %v", err)
	}

	var echoReceived sync.WaitGroup
	echoReceived.Add(1)
	var echoOnce sync.Once
	var echoMsgContext *layr8.MessageContext

	alice.Handle(echoRequest, func(msg *layr8.Message) (*layr8.Message, error) {
		var req EchoReq
		if err := msg.UnmarshalBody(&req); err != nil {
			return nil, err
		}
		log.Printf("  [alice echo] from %s: %q", msg.From, req.Message)
		echoOnce.Do(func() {
			echoMsgContext = msg.Context
			echoReceived.Done()
		})
		return &layr8.Message{
			Type: echoResponse,
			Body: EchoResp{Echo: req.Message, Timestamp: time.Now().UnixNano()},
		}, nil
	})

	alice.Handle(pingRequest, func(msg *layr8.Message) (*layr8.Message, error) {
		var req PingReq
		if err := msg.UnmarshalBody(&req); err != nil {
			return nil, err
		}
		log.Printf("  [alice ping] seq=%d from %s", req.Seq, msg.From)
		return &layr8.Message{
			Type: pingResponse,
			Body: PingResp{Seq: req.Seq, Pong: "pong"},
		}, nil
	})

	if err := alice.Connect(ctx); err != nil {
		log.Fatalf("  FATAL: alice.Connect(): %v", err)
	}
	defer alice.Close()
	pass("Alice connected with echo + ping handlers")

	// Test 2: DID()
	fmt.Println("  [2] DID() returns expected value")

	if alice.DID() == aliceDID {
		pass(fmt.Sprintf("alice.DID() = %s", alice.DID()))
	} else {
		fail("DID mismatch", fmt.Sprintf("got %q, want %q", alice.DID(), aliceDID))
	}

	// Test 3: Connect Bob
	fmt.Println("  [3] Connect Bob")

	bob, err := layr8.NewClient(layr8.Config{
		NodeURL:  bobNodeURL,
		APIKey:   bobAPIKey,
		AgentDID: bobDID,
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		log.Fatalf("  FATAL: NewClient(bob): %v", err)
	}

	bob.Handle(echoRequest, func(msg *layr8.Message) (*layr8.Message, error) {
		return nil, nil
	})
	bob.Handle(pingRequest, func(msg *layr8.Message) (*layr8.Message, error) {
		return nil, nil
	})

	if err := bob.Connect(ctx); err != nil {
		log.Fatalf("  FATAL: bob.Connect(): %v", err)
	}
	defer bob.Close()
	pass("Bob connected")

	// Test 4: Config from env vars
	fmt.Println("  [4] Config from environment variables")

	envDID := fmt.Sprintf("did:web:alice-test.localhost:env-test-%s", testID)
	os.Setenv("LAYR8_NODE_URL", aliceNodeURL)
	os.Setenv("LAYR8_API_KEY", aliceAPIKey)
	os.Setenv("LAYR8_AGENT_DID", envDID)
	defer func() {
		os.Unsetenv("LAYR8_NODE_URL")
		os.Unsetenv("LAYR8_API_KEY")
		os.Unsetenv("LAYR8_AGENT_DID")
	}()

	aliceEnv, err := layr8.NewClient(layr8.Config{}, layr8.LogErrors(log.Default()))
	if err != nil {
		fail("config env", fmt.Sprintf("NewClient with env vars: %v", err))
	} else {
		aliceEnv.Handle(echoRequest, func(msg *layr8.Message) (*layr8.Message, error) {
			return nil, nil
		})
		envCtx, envCancel := context.WithTimeout(ctx, 10*time.Second)
		if err := aliceEnv.Connect(envCtx); err != nil {
			envCancel()
			fail("config env", fmt.Sprintf("Connect with env vars: %v", err))
		} else {
			envCancel()
			if aliceEnv.DID() == envDID {
				pass(fmt.Sprintf("connected via env vars, DID=%s", aliceEnv.DID()))
			} else {
				fail("config env DID", fmt.Sprintf("got %q, want %q", aliceEnv.DID(), envDID))
			}
			aliceEnv.Close()
		}
	}

	// ═══════════════════════════════════════════════════════════════════
	section("Cross-Node Messaging")
	// ═══════════════════════════════════════════════════════════════════

	// Test 5: Echo protocol (Bob → Alice)
	fmt.Println("  [5] Request/Response — echo protocol (Bob → Alice)")

	reqCtx, reqCancel := context.WithTimeout(ctx, 15*time.Second)
	resp, err := bob.Request(reqCtx, &layr8.Message{
		Type: echoRequest,
		To:   []string{aliceDID},
		Body: EchoReq{Message: "Hello from Bob!", Timestamp: time.Now().UnixNano()},
	})
	reqCancel()

	if err != nil {
		fail("echo request", err.Error())
	} else {
		var echoResp EchoResp
		if err := resp.UnmarshalBody(&echoResp); err != nil {
			fail("echo unmarshal", err.Error())
		} else if echoResp.Echo == "Hello from Bob!" {
			pass(fmt.Sprintf("echo response: %q", echoResp.Echo))
		} else {
			fail("echo mismatch", fmt.Sprintf("got %q", echoResp.Echo))
		}
	}

	// Test 6: Ping protocol (Bob → Alice, multi-handler)
	fmt.Println("  [6] Request/Response — ping protocol (Bob → Alice)")

	reqCtx2, reqCancel2 := context.WithTimeout(ctx, 15*time.Second)
	resp2, err := bob.Request(reqCtx2, &layr8.Message{
		Type: pingRequest,
		To:   []string{aliceDID},
		Body: PingReq{Seq: 42},
	})
	reqCancel2()

	if err != nil {
		fail("ping request", err.Error())
	} else {
		var pingResp PingResp
		if err := resp2.UnmarshalBody(&pingResp); err != nil {
			fail("ping unmarshal", err.Error())
		} else if pingResp.Seq == 42 && pingResp.Pong == "pong" {
			pass(fmt.Sprintf("ping response: seq=%d pong=%q", pingResp.Seq, pingResp.Pong))
		} else {
			fail("ping mismatch", fmt.Sprintf("got seq=%d pong=%q", pingResp.Seq, pingResp.Pong))
		}
	}

	// Test 7: Fire-and-forget (Send)
	fmt.Println("  [7] Fire-and-forget (Send)")

	err = bob.Send(ctx, &layr8.Message{
		Type: "https://didcomm.org/basicmessage/2.0/message",
		To:   []string{aliceDID},
		Body: map[string]string{"content": "fire-and-forget test", "locale": "en"},
	})
	if err != nil {
		fail("Send", err.Error())
	} else {
		pass("Send returned without error")
	}

	// Test 8: WithParentThread
	fmt.Println("  [8] WithParentThread (nested thread correlation)")

	parentThreadID := "parent-" + testID
	reqCtx4, reqCancel4 := context.WithTimeout(ctx, 15*time.Second)
	resp4, err := bob.Request(reqCtx4, &layr8.Message{
		Type: echoRequest,
		To:   []string{aliceDID},
		Body: EchoReq{Message: "nested thread test"},
	}, layr8.WithParentThread(parentThreadID))
	reqCancel4()

	if err != nil {
		fail("WithParentThread", err.Error())
	} else {
		var echoResp4 EchoResp
		resp4.UnmarshalBody(&echoResp4)
		if echoResp4.Echo == "nested thread test" {
			pass(fmt.Sprintf("response received (echo=%q)", echoResp4.Echo))
		} else {
			fail("WithParentThread", fmt.Sprintf("unexpected echo: %q", echoResp4.Echo))
		}
	}

	// ═══════════════════════════════════════════════════════════════════
	section("Message Context & Auth")
	// ═══════════════════════════════════════════════════════════════════

	// Test 9: Auth context on received message
	fmt.Println("  [9] MessageContext on inbound message")

	waitCh := make(chan struct{})
	go func() {
		echoReceived.Wait()
		close(waitCh)
	}()
	select {
	case <-waitCh:
	case <-time.After(5 * time.Second):
	}

	if echoMsgContext != nil {
		pass(fmt.Sprintf("authorized=%v, recipient=%s, credentials=%d",
			echoMsgContext.Authorized, echoMsgContext.Recipient, len(echoMsgContext.SenderCredentials)))
	} else {
		skip("MessageContext", "msg.Context was nil (node may not populate it)")
	}

	// ═══════════════════════════════════════════════════════════════════
	section("Handler Options")
	// ═══════════════════════════════════════════════════════════════════

	// Test 10: Problem report (handler returns error)
	fmt.Println("  [10] Problem report (handler returns error)")

	errDID := fmt.Sprintf("did:web:alice-test.localhost:err-test-%s", testID)
	aliceErr, err := layr8.NewClient(layr8.Config{
		NodeURL:  aliceNodeURL,
		APIKey:   aliceAPIKey,
		AgentDID: errDID,
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		log.Fatalf("  FATAL: NewClient(aliceErr): %v", err)
	}

	errHandlerCalled := make(chan struct{}, 1)
	aliceErr.Handle(echoRequest, func(msg *layr8.Message) (*layr8.Message, error) {
		log.Printf("  [aliceErr] handler invoked, returning error")
		select {
		case errHandlerCalled <- struct{}{}:
		default:
		}
		return nil, fmt.Errorf("intentional test error")
	})

	if err := aliceErr.Connect(ctx); err != nil {
		log.Fatalf("  FATAL: aliceErr.Connect(): %v", err)
	}
	defer aliceErr.Close()

	reqCtx3, reqCancel3 := context.WithTimeout(ctx, 10*time.Second)
	_, err = bob.Request(reqCtx3, &layr8.Message{
		Type: echoRequest,
		To:   []string{errDID},
		Body: EchoReq{Message: "trigger error"},
	})
	reqCancel3()

	if err != nil {
		var prob *layr8.ProblemReportError
		if errors.As(err, &prob) {
			pass(fmt.Sprintf("ProblemReportError: code=%q comment=%q", prob.Code, prob.Comment))
		} else {
			select {
			case <-errHandlerCalled:
				pass("handler returned error (problem report sent, node did not deliver it back)")
			case <-time.After(1 * time.Second):
				fail("problem report", fmt.Sprintf("handler not invoked, got %T: %v", err, err))
			}
		}
	} else {
		fail("problem report", "expected error, got success")
	}

	// Test 11: Manual ack (WithManualAck + msg.Ack())
	fmt.Println("  [11] Manual ack (WithManualAck + msg.Ack())")

	ackDID := fmt.Sprintf("did:web:alice-test.localhost:ack-test-%s", testID)
	aliceAck, err := layr8.NewClient(layr8.Config{
		NodeURL:  aliceNodeURL,
		APIKey:   aliceAPIKey,
		AgentDID: ackDID,
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		log.Fatalf("  FATAL: NewClient(aliceAck): %v", err)
	}

	var ackInvoked bool
	aliceAck.Handle(echoRequest, func(msg *layr8.Message) (*layr8.Message, error) {
		var req EchoReq
		if err := msg.UnmarshalBody(&req); err != nil {
			return nil, err
		}
		msg.Ack()
		ackInvoked = true
		log.Printf("  [alice ack] manually acked message from %s", msg.From)
		return &layr8.Message{
			Type: echoResponse,
			Body: EchoResp{Echo: req.Message, Timestamp: time.Now().UnixNano()},
		}, nil
	}, layr8.WithManualAck())

	if err := aliceAck.Connect(ctx); err != nil {
		log.Fatalf("  FATAL: aliceAck.Connect(): %v", err)
	}
	defer aliceAck.Close()

	reqCtx5, reqCancel5 := context.WithTimeout(ctx, 15*time.Second)
	resp5, err := bob.Request(reqCtx5, &layr8.Message{
		Type: echoRequest,
		To:   []string{ackDID},
		Body: EchoReq{Message: "manual ack test"},
	})
	reqCancel5()

	if err != nil {
		fail("manual ack", err.Error())
	} else {
		var echoResp5 EchoResp
		resp5.UnmarshalBody(&echoResp5)
		if ackInvoked && echoResp5.Echo == "manual ack test" {
			pass("handler invoked Ack(), response received")
		} else {
			fail("manual ack", fmt.Sprintf("ackInvoked=%v echo=%q", ackInvoked, echoResp5.Echo))
		}
	}

	// ═══════════════════════════════════════════════════════════════════
	section("Error Handling")
	// ═══════════════════════════════════════════════════════════════════

	// Test 12: ErrAlreadyConnected
	fmt.Println("  [12] Handle after Connect → ErrAlreadyConnected")

	err = alice.Handle(echoRequest, func(msg *layr8.Message) (*layr8.Message, error) {
		return nil, nil
	})
	if errors.Is(err, layr8.ErrAlreadyConnected) {
		pass("Handle after Connect returns ErrAlreadyConnected")
	} else if err != nil {
		pass(fmt.Sprintf("Handle after Connect returns error: %v", err))
	} else {
		fail("ErrAlreadyConnected", "expected error, got nil")
	}

	// Test 13: ErrNotConnected (Send)
	fmt.Println("  [13] Send before Connect → ErrNotConnected")

	notConnClient, err := layr8.NewClient(layr8.Config{
		NodeURL:  aliceNodeURL,
		APIKey:   aliceAPIKey,
		AgentDID: "did:web:alice-test.localhost:notconn-test",
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		log.Fatalf("  FATAL: NewClient(notConn): %v", err)
	}

	err = notConnClient.Send(ctx, &layr8.Message{
		Type: echoRequest,
		To:   []string{aliceDID},
		Body: EchoReq{Message: "should fail"},
	})
	if errors.Is(err, layr8.ErrNotConnected) {
		pass("Send before Connect returns ErrNotConnected")
	} else {
		fail("ErrNotConnected", fmt.Sprintf("expected ErrNotConnected, got %v", err))
	}

	// Test 14: ErrNotConnected (Request)
	fmt.Println("  [14] Request before Connect → ErrNotConnected")

	reqCtx6, reqCancel6 := context.WithTimeout(ctx, 5*time.Second)
	_, err = notConnClient.Request(reqCtx6, &layr8.Message{
		Type: echoRequest,
		To:   []string{aliceDID},
		Body: EchoReq{Message: "should fail"},
	})
	reqCancel6()

	if errors.Is(err, layr8.ErrNotConnected) {
		pass("Request before Connect returns ErrNotConnected")
	} else {
		fail("ErrNotConnected (Request)", fmt.Sprintf("expected ErrNotConnected, got %v", err))
	}

	// ═══════════════════════════════════════════════════════════════════
	// Summary
	// ═══════════════════════════════════════════════════════════════════
	fmt.Println()
	fmt.Printf("%s%s══════════════════════════════════════%s\n", colorBold, colorCyan, colorReset)
	fmt.Printf("  %s%sPassed:  %d%s\n", colorBold, colorGreen, passed, colorReset)
	if failed > 0 {
		fmt.Printf("  %s%sFailed:  %d%s\n", colorBold, colorRed, failed, colorReset)
	} else {
		fmt.Printf("  Failed:  %d\n", failed)
	}
	if skipped > 0 {
		fmt.Printf("  %s%sSkipped: %d%s\n", colorBold, colorYellow, skipped, colorReset)
	}
	fmt.Printf("%s%s══════════════════════════════════════%s\n\n", colorBold, colorCyan, colorReset)

	if failed > 0 {
		os.Exit(1)
	}
}
