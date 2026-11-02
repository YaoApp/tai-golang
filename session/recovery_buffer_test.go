package session

import "testing"

func TestRecvBuffer_AppendAndLastSeq(t *testing.T) {
	rb := newRecvBuffer(5)
	if rb.LastSeq() != 0 {
		t.Fatalf("empty buffer LastSeq = %d, want 0", rb.LastSeq())
	}
	rb.Append(1, "text", []byte("hello"))
	rb.Append(2, "text", []byte("world"))
	if rb.LastSeq() != 2 {
		t.Fatalf("LastSeq = %d, want 2", rb.LastSeq())
	}
	if rb.Size() != 2 {
		t.Fatalf("Size = %d, want 2", rb.Size())
	}
}

func TestRecvBuffer_Overflow(t *testing.T) {
	rb := newRecvBuffer(3)
	rb.Append(1, "a", nil)
	rb.Append(2, "b", nil)
	rb.Append(3, "c", nil)
	rb.Append(4, "d", nil) // overwrites seq 1
	if rb.Size() != 3 {
		t.Fatalf("Size = %d, want 3", rb.Size())
	}
	if rb.LastSeq() != 4 {
		t.Fatalf("LastSeq = %d, want 4", rb.LastSeq())
	}
}

func TestRecvBuffer_DataCopy(t *testing.T) {
	rb := newRecvBuffer(5)
	original := []byte("original")
	rb.Append(1, "text", original)

	// Mutate the original — should not affect stored data.
	original[0] = 'X'
	rb.mu.Lock()
	stored := rb.buf[0].data
	rb.mu.Unlock()
	if stored[0] == 'X' {
		t.Error("buffer stores reference instead of copy")
	}
}
