package modbusclient

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/grid-x/modbus"
)

// Session holds a connected Modbus TCP client for one target.
type Session struct {
	handler *modbus.TCPClientHandler
	client  modbus.Client
	unitID  byte
	mock    bool
}

type DialTarget struct {
	Host           string
	Port           int
	UnitID         int
	ConnectTimeout time.Duration
	RequestTimeout time.Duration
}

func Dial(ctx context.Context, target DialTarget) (*Session, error) {
	if isMockHost(target.Host) {
		return &Session{mock: true}, nil
	}

	addr := fmt.Sprintf("%s:%d", target.Host, target.Port)
	h := modbus.NewTCPClientHandler(addr)
	h.Timeout = target.RequestTimeout
	h.IdleTimeout = -1
	h.SetSlave(byte(target.UnitID))

	dctx, cancel := context.WithTimeout(ctx, target.ConnectTimeout)
	defer cancel()
	if err := h.Connect(dctx); err != nil {
		return nil, err
	}
	return &Session{handler: h, client: modbus.NewClient(h), unitID: byte(target.UnitID)}, nil
}

func (s *Session) Close() error {
	if s == nil || s.handler == nil {
		return nil
	}
	return s.handler.Close()
}

// ReadHolding performs FC3 read (quantity registers from start).
func (s *Session) ReadHolding(ctx context.Context, start, quantity uint16) ([]byte, error) {
	if s != nil && s.mock {
		return mockPayload(start, quantity), nil
	}
	return s.client.ReadHoldingRegisters(ctx, start, quantity)
}

// ReadHoldingUnit is ReadHolding addressed to another unit id behind
// the same TCP endpoint (a meter on the SmartLogger's RS485 bus). The
// session's own unit id is restored afterwards; not safe for
// concurrent use, like the rest of the session.
func (s *Session) ReadHoldingUnit(ctx context.Context, unitID byte, start, quantity uint16) ([]byte, error) {
	if s != nil && s.mock {
		return mockPayload(start, quantity), nil
	}
	s.handler.SetSlave(unitID)
	defer s.handler.SetSlave(s.unitID)
	return s.client.ReadHoldingRegisters(ctx, start, quantity)
}

// ExceptionCode returns the Modbus exception code when err is an
// exception response: the device answered and refused the request, so
// the TCP stream is intact (unlike a timeout or I/O error).
func ExceptionCode(err error) (byte, bool) {
	var me *modbus.Error
	if errors.As(err, &me) {
		return me.ExceptionCode, true
	}
	return 0, false
}

// ReadInput performs FC4 read.
func (s *Session) ReadInput(ctx context.Context, start, quantity uint16) ([]byte, error) {
	if s != nil && s.mock {
		return mockPayload(start, quantity), nil
	}
	return s.client.ReadInputRegisters(ctx, start, quantity)
}

func isMockHost(host string) bool {
	v := strings.ToLower(strings.TrimSpace(host))
	return v == "mock" || v == "mocked" || v == "simulator"
}

func mockPayload(start, quantity uint16) []byte {
	b := make([]byte, int(quantity)*2)
	for i := uint16(0); i < quantity; i++ {
		// Deterministic synthetic register value per address.
		v := start + i + 1
		binary.BigEndian.PutUint16(b[int(i)*2:], v)
	}
	return b
}
