// Durable Handler — persist messages to a file before acknowledging.
//
// Demonstrates manual ack: messages are only acknowledged after they
// are safely written to disk. If the process crashes between receive
// and ack, the cloud-node redelivers the message.
//
// Messages are appended as JSON lines to messages.jsonl.
//
// Usage:
//
//	LAYR8_API_KEY=your-key go run ./examples/durable-handler
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"sync"

	layr8 "github.com/layr8/go-sdk"
)

const filePath = "messages.jsonl"

type record struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	From string `json:"from"`
	Body any    `json:"body"`
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("open file: %v", err)
	}
	defer f.Close()
	var mu sync.Mutex

	client, err := layr8.NewClient(layr8.Config{})
	if err != nil {
		log.Fatal(err)
	}

	client.Handle("https://layr8.io/protocols/order/1.0/created",
		func(msg *layr8.Message) (*layr8.Message, error) {
			var body any
			_ = msg.UnmarshalBody(&body)

			line, err := json.Marshal(record{
				ID:   msg.ID,
				Type: msg.Type,
				From: msg.From,
				Body: body,
			})
			if err != nil {
				return nil, err
			}
			line = append(line, '\n')

			// Persist first — if this fails, the message is NOT acked
			// and the cloud-node will redeliver it.
			mu.Lock()
			_, err = f.Write(line)
			if err == nil {
				err = f.Sync()
			}
			mu.Unlock()
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

	log.Printf("durable handler running (DID=%s), persisting to %s", client.DID(), filePath)
	<-ctx.Done()
	log.Println("shutting down")
}
