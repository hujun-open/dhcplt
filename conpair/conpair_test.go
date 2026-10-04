package conpair

import (
	"strings"
	"testing"
	"time"
)

func TestPacketConnPairReadWrite(t *testing.T) {
	a, b := NewPacketConnPair()

	if _, err := a.WriteTo([]byte("hello"), nil); err != nil {
		t.Fatalf("WriteTo error: %v", err)
	}
	buf := make([]byte, 16)
	n, _, err := b.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom error: %v", err)
	}
	if got := string(buf[:n]); got != "hello" {
		t.Errorf("ReadFrom = %q, want %q", got, "hello")
	}
}

func TestPacketConnPairIsBidirectional(t *testing.T) {
	a, b := NewPacketConnPair()

	if _, err := b.WriteTo([]byte("ping"), nil); err != nil {
		t.Fatalf("WriteTo error: %v", err)
	}
	buf := make([]byte, 16)
	n, _, err := a.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom error: %v", err)
	}
	if got := string(buf[:n]); got != "ping" {
		t.Errorf("ReadFrom = %q, want %q", got, "ping")
	}
}

func TestPacketConnPairReadTimeout(t *testing.T) {
	_, b := NewPacketConnPair()

	if err := b.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline error: %v", err)
	}
	start := time.Now()
	_, _, err := b.ReadFrom(make([]byte, 16))
	if err == nil {
		t.Fatal("ReadFrom = nil error, want timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("ReadFrom error = %q, want it to mention timeout", err.Error())
	}
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Errorf("ReadFrom returned after %v, want it to wait for the deadline", elapsed)
	}
}

func TestPacketConnPairWriteTimeout(t *testing.T) {
	a, _ := NewPacketConnPair()

	// Fill the peer's receive buffer so the next write would block.
	for i := 0; i < maxChDepth; i++ {
		if _, err := a.WriteTo([]byte{byte(i)}, nil); err != nil {
			t.Fatalf("WriteTo(%d) error: %v", i, err)
		}
	}
	if err := a.SetWriteDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("SetWriteDeadline error: %v", err)
	}
	_, err := a.WriteTo([]byte{0}, nil)
	if err == nil {
		t.Fatal("WriteTo = nil error, want timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("WriteTo error = %q, want it to mention timeout", err.Error())
	}
}

func TestTimeoutErrMethods(t *testing.T) {
	te := timeoutErr("boom")
	if got := te.Error(); got != "boom" {
		t.Errorf("Error() = %q, want %q", got, "boom")
	}
	if !(&te).Timeout() {
		t.Error("Timeout() = false, want true")
	}
	if !(&te).Temporary() {
		t.Error("Temporary() = false, want true")
	}
}

func TestClose(t *testing.T) {
	a, b := NewPacketConnPair()
	if err := a.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
	// closing a closes b's receive channel, so a read returns immediately.
	n, _, err := b.ReadFrom(make([]byte, 8))
	if err != nil {
		t.Fatalf("ReadFrom after Close error: %v", err)
	}
	if n != 0 {
		t.Errorf("ReadFrom after Close n = %d, want 0", n)
	}
}
