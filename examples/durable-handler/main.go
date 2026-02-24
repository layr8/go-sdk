// Durable Handler — persist messages to SQLite before acknowledging.
//
// Demonstrates manual ack: messages are only acknowledged after they
// are safely written to disk. If the process crashes between receive
// and ack, the cloud-node redelivers the message.
//
// Usage:
//
//	LAYR8_API_KEY=your-key go run ./examples/durable-handler
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"os/signal"

	layr8 "github.com/layr8/go-sdk"
	_ "modernc.org/sqlite"
)

const dbPath = "messages.db"

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id       TEXT PRIMARY KEY,
			type     TEXT NOT NULL,
			from_did TEXT NOT NULL,
			body     TEXT NOT NULL
		)
	`); err != nil {
		log.Fatalf("create table: %v", err)
	}

	client, err := layr8.NewClient(layr8.Config{})
	if err != nil {
		log.Fatal(err)
	}

	client.Handle("https://layr8.io/protocols/order/1.0/created",
		func(msg *layr8.Message) (*layr8.Message, error) {
			bodyJSON, err := json.Marshal(msg.Body)
			if err != nil {
				return nil, err
			}

			// Persist first — if this fails, the message is NOT acked
			// and the cloud-node will redeliver it.
			_, err = db.Exec(
				`INSERT OR IGNORE INTO messages (id, type, from_did, body) VALUES (?, ?, ?, ?)`,
				msg.ID, msg.Type, msg.From, string(bodyJSON),
			)
			if err != nil {
				return nil, err // not acked — will be redelivered
			}

			msg.Ack() // safe to ack now
			log.Printf("persisted and acked message %s from %s", msg.ID, msg.From)
			return nil, nil
		},
		layr8.WithManualAck(),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := client.Connect(ctx); err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	log.Printf("durable handler running (DID=%s), persisting to %s", client.DID(), dbPath)
	<-ctx.Done()
	log.Println("shutting down")
}
