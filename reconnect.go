package layr8

import "time"

// backoff implements exponential backoff with a maximum delay.
type backoff struct {
	initial time.Duration
	max     time.Duration
	current time.Duration
}

func newBackoff(initial, max time.Duration) *backoff {
	return &backoff{
		initial: initial,
		max:     max,
		current: initial,
	}
}

func (b *backoff) next() time.Duration {
	d := b.current
	b.current *= 2
	if b.current > b.max {
		b.current = b.max
	}
	if d > b.max {
		d = b.max
	}
	return d
}

func (b *backoff) reset() {
	b.current = b.initial
}
