package layr8

import (
	"testing"
	"time"
)

func TestBackoff_ExponentialWithCap(t *testing.T) {
	b := newBackoff(1*time.Second, 30*time.Second)

	d1 := b.next()
	if d1 != 1*time.Second {
		t.Errorf("first backoff = %v, want 1s", d1)
	}

	d2 := b.next()
	if d2 != 2*time.Second {
		t.Errorf("second backoff = %v, want 2s", d2)
	}

	d3 := b.next()
	if d3 != 4*time.Second {
		t.Errorf("third backoff = %v, want 4s", d3)
	}

	d4 := b.next()
	if d4 != 8*time.Second {
		t.Errorf("fourth backoff = %v, want 8s", d4)
	}

	d5 := b.next()
	if d5 != 16*time.Second {
		t.Errorf("fifth backoff = %v, want 16s", d5)
	}

	d6 := b.next()
	if d6 != 30*time.Second {
		t.Errorf("sixth backoff = %v, want 30s (capped)", d6)
	}

	d7 := b.next()
	if d7 != 30*time.Second {
		t.Errorf("seventh backoff = %v, want 30s (capped)", d7)
	}
}

func TestBackoff_Reset(t *testing.T) {
	b := newBackoff(1*time.Second, 30*time.Second)

	b.next() // 1s
	b.next() // 2s
	b.next() // 4s

	b.reset()

	d := b.next()
	if d != 1*time.Second {
		t.Errorf("after reset, backoff = %v, want 1s", d)
	}
}
