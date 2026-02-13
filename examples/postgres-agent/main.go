// Example: Postgres Agent (Resource Side)
//
// Rewritten from the layr8-demos postgres agent.
// The original is hundreds of lines managing WebSocket, Phoenix Channels,
// DIDComm envelope construction, heartbeats, reconnection, and thread correlation.
// With the SDK, the agent focuses purely on its domain logic.
//
// Demonstrates: Handle (request/response), Request (outbound), manual ack,
// concurrent fan-out, MessageContext for authorization.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"

	layr8 "github.com/layr8/layr8-go-sdk"
	_ "github.com/lib/pq"
)

// --- Protocol types ---

const (
	ProtocolBase       = "https://layr8.io/protocols/postgres/1.0"
	ProtocolQueryType  = ProtocolBase + "/query"
	ProtocolResultType = ProtocolBase + "/result"
)

type QueryRequest struct {
	Action   string          `json:"action"`   // "read" or "write"
	Resource string          `json:"resource"` // target resource DID path
	Query    string          `json:"query"`    // SQL statement
	Params   json.RawMessage `json:"params"`   // query parameters
	Reason   string          `json:"reason"`   // audit reason
}

type QueryResponse struct {
	Decision string     `json:"decision"` // "allow" or "deny"
	Columns  []string   `json:"columns"`
	Rows     [][]any    `json:"rows"`
	Explain  string     `json:"explain"` // policy decision explanation
}

func main() {
	// Connect to local PostgreSQL
	db, err := sql.Open("postgres", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Create Layr8 client
	client, err := layr8.NewClient(layr8.Config{
		NodeURL:  "ws://localhost:4000/plugin_socket/websocket",
		AgentDID: "did:web:mycompany:postgres-agent",
		// APIKey loaded from LAYR8_API_KEY env
	})
	if err != nil {
		log.Fatal(err)
	}

	// Handle incoming query requests with manual ack
	// We only ack after successful execution to enable redelivery on failure.
	client.Handle(ProtocolQueryType,
		func(msg *layr8.Message) (*layr8.Message, error) {
			var req QueryRequest
			if err := msg.UnmarshalBody(&req); err != nil {
				return nil, fmt.Errorf("invalid query request: %w", err)
			}

			log.Printf("query from %s: %s (action=%s)", msg.From, req.Query, req.Action)

			// Execute query
			resp, err := executeQuery(db, req)
			if err != nil {
				return nil, fmt.Errorf("query execution failed: %w", err)
			}

			// Ack after successful execution
			msg.Ack()

			return &layr8.Message{
				Type: ProtocolResultType,
				Body: resp,
			}, nil
		},
		layr8.WithManualAck(),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := client.Connect(ctx); err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	log.Println("postgres agent running, press Ctrl+C to stop")
	<-ctx.Done()
}

func executeQuery(db *sql.DB, req QueryRequest) (*QueryResponse, error) {
	action := strings.ToUpper(strings.Fields(req.Query)[0])

	switch action {
	case "SELECT":
		return executeSelect(db, req)
	case "INSERT", "UPDATE", "DELETE":
		return executeExec(db, req)
	default:
		return &QueryResponse{
			Decision: "deny",
			Explain:  fmt.Sprintf("unsupported SQL action: %s", action),
		}, nil
	}
}

func executeSelect(db *sql.DB, req QueryRequest) (*QueryResponse, error) {
	rows, err := db.Query(req.Query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var resultRows [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		resultRows = append(resultRows, values)
	}

	return &QueryResponse{
		Decision: "allow",
		Columns:  columns,
		Rows:     resultRows,
	}, nil
}

func executeExec(db *sql.DB, req QueryRequest) (*QueryResponse, error) {
	_, err := db.Exec(req.Query)
	if err != nil {
		return nil, err
	}

	return &QueryResponse{
		Decision: "allow",
	}, nil
}
