// Copyright (c) 2021 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package thriftudp

import (
	"bytes"
	"context"
	"net"

	"github.com/uber-go/tally/v4/thirdparty/github.com/apache/thrift/lib/go/thrift"
	"go.uber.org/atomic"
)

// MaxLength of UDP packet
const MaxLength = 65000

// Ensure TUDPTransport implements TRichTransport which avoids allocations
// when writing strings.
var _ thrift.TRichTransport = (*TUDPTransport)(nil)

// TUDPTransport does UDP as a thrift.TTransport
type TUDPTransport struct {
	conn        *net.UDPConn
	addr        net.Addr
	writeBuf    bytes.Buffer
	readByteBuf []byte
	closed      atomic.Bool
}

// NewTUDPClientTransport creates a net.UDPConn-backed TTransport for Thrift clients
// All writes are buffered and flushed in one UDP packet. If locHostPort is not "", it
// will be used as the local address for the connection
// Example:
// 	trans, err := thriftudp.NewTUDPClientTransport("192.168.1.1:9090", "")
func NewTUDPClientTransport(destHostPort string, locHostPort string) (*TUDPTransport, error) {
	destAddr, err := net.ResolveUDPAddr("udp", destHostPort)
	if err != nil {
		return nil, thrift.NewTTransportException(thrift.NOT_OPEN, err.Error())
	}

	var locAddr *net.UDPAddr
	if locHostPort != "" {
		locAddr, err = net.ResolveUDPAddr("udp", locHostPort)
		if err != nil {
			return nil, thrift.NewTTransportException(thrift.NOT_OPEN, err.Error())
		}
	}

	conn, err := net.DialUDP(destAddr.Network(), locAddr, destAddr)
	if err != nil {
		return nil, thrift.NewTTransportException(thrift.NOT_OPEN, err.Error())
	}

	return &TUDPTransport{
		addr:        destAddr,
		conn:        conn,
		readByteBuf: make([]byte, 1),
	}, nil
}

// NewTUDPServerTransport creates a net.UDPConn-backed TTransport for Thrift servers
// It will listen for incoming udp packets on the specified host/port
// Example:
// 	trans, err := thriftudp.NewTUDPClientTransport("localhost:9001")
func NewTUDPServerTransport(hostPort string) (*TUDPTransport, error) {
	return NewTUDPServerTransportWithListenConfig(hostPort, net.ListenConfig{})
}

// NewTUDPServerTransportWithListenConfig creates a net.UDPConn-backed TTransport for Thrift servers
// It will listen for incoming udp packets on the specified host/port
// It takes net.ListenConfig to customize socket options
func NewTUDPServerTransportWithListenConfig(
	hostPort string,
	listenConfig net.ListenConfig,
) (*TUDPTransport, error) {
	conn, err := listenConfig.ListenPacket(context.Background(), "udp", hostPort)
	if err != nil {
		return nil, thrift.NewTTransportException(thrift.NOT_OPEN, err.Error())
	}

	uconn, ok := conn.(*net.UDPConn)
	if !ok {
		return nil, thrift.NewTTransportException(thrift.NOT_OPEN, "not a udp connection")
	}

	return &TUDPTransport{
		addr:        uconn.LocalAddr(),
		conn:        uconn,
		readByteBuf: make([]byte, 1),
	}, nil
}

// Open does nothing as connection is opened on creation
// Required to maintain thrift.TTransport interface
func (p *TUDPTransport) Open() error {
	return nil
}

// Conn retrieves the underlying net.UDPConn
func (p *TUDPTransport) Conn() *net.UDPConn {
	return p.conn
}

// IsOpen returns true if the connection is open
func (p *TUDPTransport) IsOpen() bool {
	return !p.closed.Load()
}

// Close closes the transport and the underlying connection.
// Note: the current implementation allows Close to be called multiple times without an error.
func (p *TUDPTransport) Close() error {
	if closed := p.closed.Swap(true); !closed {
		return p.conn.Close()
	}
	return nil
}

// Addr returns the address that the transport is listening on or writing to
func (p *TUDPTransport) Addr() net.Addr {
	return p.addr
}

// Read reads one UDP packet and puts it in the specified buf
func (p *TUDPTransport) Read(buf []byte) (int, error) {
	if !p.IsOpen() {
		return 0, thrift.NewTTransportException(thrift.NOT_OPEN, "Connection not open")
	}
	n, err := p.conn.Read(buf)
	return n, thrift.NewTTransportExceptionFromError(err)
}

// ReadByte reads a single byte and returns it
func (p *TUDPTransport) ReadByte() (byte, error) {
	n, err := p.Read(p.readByteBuf)
	if err != nil {
		return 0, err
	}

	if n == 0 {
		return 0, thrift.NewTTransportException(thrift.PROTOCOL_ERROR, "Received empty packet")
	}

	return p.readByteBuf[0], nil
}

// RemainingBytes returns the max number of bytes (same as Thrift's StreamTransport) as we
// do not know how many bytes we have left.
func (p *TUDPTransport) RemainingBytes() uint64 {
	const maxSize = ^uint64(0)
	return maxSize
}

// Write writes specified buf to the write buffer
func (p *TUDPTransport) Write(buf []byte) (int, error) {
	if !p.IsOpen() {
		return 0, thrift.NewTTransportException(thrift.NOT_OPEN, "Connection not open")
	}
	if p.writeBuf.Len()+len(buf) > MaxLength {
		return 0, thrift.NewTTransportException(thrift.INVALID_DATA, "Data does not fit within one UDP packet")
	}
	n, err := p.writeBuf.Write(buf)
	return n, thrift.NewTTransportExceptionFromError(err)
}

// WriteByte writes a single byte to the write buffer
func (p *TUDPTransport) WriteByte(b byte) error {
	if !p.IsOpen() {
		return thrift.NewTTransportException(thrift.NOT_OPEN, "Connection not open")
	}
	if p.writeBuf.Len()+1 > MaxLength {
		return thrift.NewTTransportException(thrift.INVALID_DATA, "Data does not fit within one UDP packet")
	}

	err := p.writeBuf.WriteByte(b)
	return thrift.NewTTransportExceptionFromError(err)
}

// WriteString writes the specified string to the write buffer
func (p *TUDPTransport) WriteString(s string) (int, error) {
	if !p.IsOpen() {
		return 0, thrift.NewTTransportException(thrift.NOT_OPEN, "Connection not open")
	}
	if p.writeBuf.Len()+len(s) > MaxLength {
		return 0, thrift.NewTTransportException(thrift.INVALID_DATA, "Data does not fit within one UDP packet")
	}

	n, err := p.writeBuf.WriteString(s)
	return n, thrift.NewTTransportExceptionFromError(err)
}

// Flush flushes the write buffer as one udp packet
func (p *TUDPTransport) Flush() error {
	if !p.IsOpen() {
		return thrift.NewTTransportException(thrift.NOT_OPEN, "Connection not open")
	}

	_, err := p.conn.Write(p.writeBuf.Bytes())
	p.writeBuf.Reset() // always reset the buffer, even in case of an error
	return err
}
