package thriftudp

import (
	"fmt"

	"github.com/apache/thrift/lib/go/thrift"
)

// TMultiUDPTransport does multiUDP as a thrift.TTransport
type TMultiUDPTransport struct {
	transports []thrift.TTransport
}

// NewTMultiUDPClientTransport creates a set of net.UDPConn-backed TTransports for Thrift clients
// All writes are buffered and flushed in one UDP packet. If locHostPort is not "", it
// will be used as the local address for the connection
// Example:
// 	trans, err := thriftudp.NewTMultiUDPClientTransport([]string{"192.168.1.1:9090","192.168.1.2:9090"}, "")
func NewTMultiUDPClientTransport(destHostPorts []string, locHostPort string) (*TMultiUDPTransport, error) {
	var transports []thrift.TTransport
	for i := range destHostPorts {
		trans, err := NewTUDPClientTransport(destHostPorts[i], locHostPort)
		if err != nil {
			return nil, err
		}
		transports = append(transports, trans)
	}

	return &TMultiUDPTransport{transports: transports}, nil
}

// Open the connections of the underlying transports
func (p *TMultiUDPTransport) Open() error {
	for _, trans := range p.transports {
		if err := trans.Open(); err != nil {
			return err
		}
	}
	return nil
}

// IsOpen returns true if the connections of the underlying transports are open
func (p *TMultiUDPTransport) IsOpen() bool {
	for _, trans := range p.transports {
		if open := trans.IsOpen(); !open {
			return false
		}
	}
	return true
}

// Close closes the connections of the underlying transports
func (p *TMultiUDPTransport) Close() error {
	for _, trans := range p.transports {
		if err := trans.Close(); err != nil {
			return err
		}
	}
	return nil
}

// Read is not supported for multiple underlying transports
func (p *TMultiUDPTransport) Read(buf []byte) (int, error) {
	// Not applicable, required by TTransport however
	return 0, fmt.Errorf("not supported")
}

// RemainingBytes is not supported for multiple underlying transports
func (p *TMultiUDPTransport) RemainingBytes() uint64 {
	// Not applicable, required by TTransport however
	return 0
}

// Write writes specified buf to the write buffer of underlying transports
func (p *TMultiUDPTransport) Write(buff []byte) (int, error) {
	n := 0
	for _, trans := range p.transports {
		written, err := trans.Write(buff)
		if err != nil {
			return n, err
		}
		if written > n {
			n = written
		}
	}
	return n, nil
}

// Flush flushes the write buffer of the underlying transports
func (p *TMultiUDPTransport) Flush() error {
	for _, trans := range p.transports {
		if err := trans.Flush(); err != nil {
			return err
		}
	}
	return nil
}
