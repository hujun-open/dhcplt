package main

import (
	"bytes"
	"encoding/gob"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
)

type myDHCPv4Lease nclient4.Lease

type exportLease struct {
	Offer        []byte
	ACK          []byte
	CreationTime time.Time
}

// MarshalBinary implements encoding.BinaryMarshaler interface
func (l myDHCPv4Lease) MarshalBinary() (data []byte, err error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	exportL := exportLease{
		Offer:        l.Offer.ToBytes(),
		ACK:          l.ACK.ToBytes(),
		CreationTime: l.CreationTime,
	}
	err = enc.Encode(exportL)
	return buf.Bytes(), err
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler interface
func (l *myDHCPv4Lease) UnmarshalBinary(data []byte) error {
	dec := gob.NewDecoder(bytes.NewBuffer(data))
	exportL := &exportLease{
		Offer: []byte{},
		ACK:   []byte{},
	}
	var err error
	if err = dec.Decode(exportL); err != nil {
		return err
	}
	if l.Offer, err = dhcpv4.FromBytes(exportL.Offer); err != nil {
		return err
	}
	if l.ACK, err = dhcpv4.FromBytes(exportL.ACK); err != nil {
		return err
	}
	l.CreationTime = exportL.CreationTime
	return nil
}
