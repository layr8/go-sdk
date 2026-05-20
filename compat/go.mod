module github.com/layr8/go-sdk/compat

go 1.25.5

replace github.com/layr8/go-sdk => ../

require github.com/layr8/go-sdk v0.0.0-00010101000000-000000000000

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
)
