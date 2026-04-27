package modbusclient

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/grid-x/modbus"

	"github.com/nesh/sestelemetry/internal/config"
)

// Session holds a connected Modbus TCP client for one target.
type Session struct {
	handler *modbus.TCPClientHandler
	client  modbus.Client
	mock    bool
}

func Dial(ctx context.Context, o config.Organization) (*Session, error) {
	if isMockHost(o.Modbus.Host) {
		return &Session{mock: true}, nil
	}

	addr := fmt.Sprintf("%s:%d", o.Modbus.Host, o.Modbus.Port)
	h := modbus.NewTCPClientHandler(addr)
	h.Timeout = o.Modbus.RequestTimeout
	h.IdleTimeout = -1
	h.SetSlave(byte(o.Modbus.UnitID))

	dctx, cancel := context.WithTimeout(ctx, o.Modbus.ConnectTimeout)
	defer cancel()
	if err := h.Connect(dctx); err != nil {
		return nil, err
	}
	return &Session{handler: h, client: modbus.NewClient(h)}, nil
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
