// conpair
package conpair

import (
	"net"
	"time"
)

const (
	maxInt64   = 9223372036854775807
	maxChDepth = 32
)

type timeoutErr string

func (terr *timeoutErr) Timeout() bool {
	return true
}
func (terr *timeoutErr) Temporary() bool {
	return true
}
func (terr timeoutErr) Error() string {
	return string(terr)
}

type PacketConnPair struct {
	peer                *PacketConnPair
	ch                  chan []byte
	readTimer, wrtTimer *time.Timer
}

func NewPacketConnPair() (A, B *PacketConnPair) {
	A = new(PacketConnPair)
	B = new(PacketConnPair)
	A.peer = B
	B.peer = A
	A.ch = make(chan []byte, maxChDepth)
	A.readTimer = time.NewTimer(maxInt64)
	A.wrtTimer = time.NewTimer(maxInt64)
	B.ch = make(chan []byte, maxChDepth)
	B.readTimer = time.NewTimer(maxInt64)
	B.wrtTimer = time.NewTimer(maxInt64)
	return
}

func (pcp *PacketConnPair) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	select {
	case <-pcp.readTimer.C:
		return 0, nil, timeoutErr("read timeout")
	case buf := <-pcp.ch:
		n = copy(p, buf)
		return
	}

}
func (pcp *PacketConnPair) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	select {
	case <-pcp.wrtTimer.C:
		return 0, timeoutErr("write timeout")
	case pcp.peer.ch <- p:
		n = len(p)
		return
	}
}

func (pcp *PacketConnPair) Close() error {
	close(pcp.peer.ch)
	return nil
}

func (pcp *PacketConnPair) LocalAddr() net.Addr {
	return nil
}

func (pcp *PacketConnPair) SetReadDeadline(t time.Time) error {
	if !pcp.readTimer.Stop() {
		<-pcp.readTimer.C
	}
	pcp.readTimer.Reset(t.Sub(time.Now()))
	return nil
}

func (pcp *PacketConnPair) SetWriteDeadline(t time.Time) error {
	if !pcp.wrtTimer.Stop() {
		<-pcp.wrtTimer.C
	}
	pcp.wrtTimer.Reset(t.Sub(time.Now()))
	return nil
}

func (pcp *PacketConnPair) SetDeadline(t time.Time) error {
	pcp.SetReadDeadline(t)
	pcp.SetWriteDeadline(t)
	return nil
}
