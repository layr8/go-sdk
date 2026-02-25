// Package layr8 provides a Go SDK for building DIDComm agents on the Layr8 platform.
//
// The SDK abstracts Phoenix Channel/WebSocket transport and DIDComm message
// formatting, exposing three core operations:
//
//   - Send: fire-and-forget messaging
//   - Request: request/response with automatic thread correlation
//   - Handle: register handlers for inbound message types
//
// Basic usage:
//
//	client, err := layr8.NewClient(layr8.Config{
//	    NodeURL:  "ws://localhost:4000/plugin_socket/websocket",
//	    AgentDID: "did:web:mycompany:my-agent",
//	}, layr8.LogErrors(log.Default()))
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	client.Handle("https://layr8.io/protocols/echo/1.0/request",
//	    func(msg *layr8.Message) (*layr8.Message, error) {
//	        return &layr8.Message{
//	            Type: "https://layr8.io/protocols/echo/1.0/response",
//	            Body: map[string]string{"echo": "hello"},
//	        }, nil
//	    },
//	)
//
//	if err := client.Connect(ctx); err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Close()
package layr8
