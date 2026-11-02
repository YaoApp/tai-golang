// Package session — recovery_buffer.go provides a per-Session ring buffer
// that stores received events by seq number. Used to verify seq continuity
// after resume and to detect recovery_gap.
package session

import "sync"

const defaultRecvBufferCapacity = 1000

// recvBuffer is a ring buffer of received data stream events indexed by seq.
type recvBuffer struct {
	mu   sync.Mutex
	buf  []recvEntry
	cap  int
	head int
	size int
}

type recvEntry struct {
	seq  uint64
	kind string
	data []byte
}

func newRecvBuffer(capacity int) *recvBuffer {
	return &recvBuffer{
		buf: make([]recvEntry, capacity),
		cap: capacity,
	}
}

// Append stores an event. Overwrites oldest when full.
func (rb *recvBuffer) Append(seq uint64, kind string, data []byte) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	idx := (rb.head + rb.size) % rb.cap
	if rb.size == rb.cap {
		rb.head = (rb.head + 1) % rb.cap
	} else {
		rb.size++
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	rb.buf[idx] = recvEntry{seq: seq, kind: kind, data: cp}
}

// LastSeq returns the highest seq stored, or 0 if empty.
func (rb *recvBuffer) LastSeq() uint64 {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if rb.size == 0 {
		return 0
	}
	idx := (rb.head + rb.size - 1) % rb.cap
	return rb.buf[idx].seq
}

// Size returns the number of stored entries.
func (rb *recvBuffer) Size() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.size
}
