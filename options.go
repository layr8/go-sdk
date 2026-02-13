package layr8

// HandlerOption configures handler behavior.
type HandlerOption func(*handlerOptions)

type handlerOptions struct {
	manualAck bool
}

func handlerDefaults() handlerOptions {
	return handlerOptions{
		manualAck: false,
	}
}

// WithManualAck disables auto-acknowledgment for a handler.
// The handler must call msg.Ack() explicitly after successful processing.
func WithManualAck() HandlerOption {
	return func(o *handlerOptions) {
		o.manualAck = true
	}
}

// RequestOption configures request behavior.
type RequestOption func(*requestOptions)

type requestOptions struct {
	parentThreadID string
}

func requestDefaults() requestOptions {
	return requestOptions{}
}

// WithParentThread sets the parent thread ID (pthid) for nested thread correlation.
func WithParentThread(pthid string) RequestOption {
	return func(o *requestOptions) {
		o.parentThreadID = pthid
	}
}
