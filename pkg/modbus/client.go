// Package modbus provides a Modbus client wrapper with state management for plcgo.
package modbus

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"time"

	modbusgo "github.com/adibhanna/modbus-go"
	"github.com/adibhanna/modbus-go/transport"
	"github.com/adibhanna/plcgo/pkg/plc"
)

// Client wraps the modbus-go client with connection state management.
type Client struct {
	config  plc.ModbusSourceConfig
	client  *modbusgo.Client
	state   plc.ConnectionState
	error   *plc.ErrorInfo
	mu      sync.RWMutex

	retryCount   int
	retryTimeout *time.Timer
	stopCh       chan struct{}

	// Callbacks
	onConnect    func()
	onDisconnect func()
	onError      func(error)
}

// NewClient creates a new Modbus client from configuration.
func NewClient(config plc.ModbusSourceConfig) *Client {
	return &Client{
		config: config,
		state:  plc.StateDisconnected,
		stopCh: make(chan struct{}),
	}
}

// SetCallbacks sets the connection state change callbacks.
func (c *Client) SetCallbacks(onConnect, onDisconnect func(), onError func(error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onConnect = onConnect
	c.onDisconnect = onDisconnect
	c.onError = onError
}

// Connect establishes the connection to the Modbus server.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.state == plc.StateConnected && c.client != nil && c.client.IsConnected() {
		return nil
	}

	c.state = plc.StateConnecting

	// Create transport
	address := fmt.Sprintf("%s:%d", c.config.Host, c.config.Port)
	t := transport.NewTCPTransport(address)

	// Create client
	c.client = modbusgo.NewClient(t)
	c.client.SetSlaveID(modbusgo.SlaveID(c.config.UnitID))

	if c.config.Timeout.Duration() > 0 {
		c.client.SetTimeout(c.config.Timeout.Duration())
	}

	// Connect
	if err := c.client.Connect(); err != nil {
		c.state = plc.StateError
		c.error = &plc.ErrorInfo{
			Message:   err.Error(),
			Timestamp: time.Now(),
		}
		c.scheduleRetry()
		return err
	}

	c.state = plc.StateConnected
	c.error = nil
	c.retryCount = 0

	if c.onConnect != nil {
		go c.onConnect()
	}

	return nil
}

// Disconnect closes the connection.
func (c *Client) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.retryTimeout != nil {
		c.retryTimeout.Stop()
		c.retryTimeout = nil
	}

	if c.client != nil {
		if err := c.client.Close(); err != nil {
			return err
		}
	}

	c.state = plc.StateDisconnected
	c.client = nil

	if c.onDisconnect != nil {
		go c.onDisconnect()
	}

	return nil
}

// State returns the current connection state.
func (c *Client) State() plc.ConnectionState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// Error returns the last error info.
func (c *Client) Error() *plc.ErrorInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.error
}

// IsConnected returns true if the client is connected.
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state == plc.StateConnected && c.client != nil && c.client.IsConnected()
}

// scheduleRetry schedules a reconnection attempt with exponential backoff.
func (c *Client) scheduleRetry() {
	minDelay := c.config.RetryMinDelay.Duration()
	maxDelay := c.config.RetryMaxDelay.Duration()

	if minDelay == 0 {
		minDelay = time.Second
	}
	if maxDelay == 0 {
		maxDelay = 30 * time.Second
	}

	// Exponential backoff
	delay := time.Duration(float64(minDelay) * math.Pow(2, float64(c.retryCount)))
	if delay > maxDelay {
		delay = maxDelay
	}
	c.retryCount++

	c.retryTimeout = time.AfterFunc(delay, func() {
		c.mu.Lock()
		if c.state == plc.StateError {
			c.mu.Unlock()
			if err := c.Connect(context.Background()); err != nil {
				if c.onError != nil {
					c.onError(err)
				}
			}
		} else {
			c.mu.Unlock()
		}
	})
}

// Read reads a value from a Modbus register based on the variable source configuration.
func (c *Client) Read(source *plc.VariableSource) (interface{}, error) {
	c.mu.RLock()
	client := c.client
	state := c.state
	c.mu.RUnlock()

	if state != plc.StateConnected || client == nil || !client.IsConnected() {
		return nil, fmt.Errorf("modbus client not connected")
	}

	address := modbusgo.Address(source.Register)

	switch source.RegisterType {
	case plc.ModbusCoil:
		vals, err := client.ReadCoils(address, 1)
		if err != nil {
			return nil, err
		}
		if len(vals) > 0 {
			return vals[0], nil
		}
		return false, nil

	case plc.ModbusDiscreteInput:
		vals, err := client.ReadDiscreteInputs(address, 1)
		if err != nil {
			return nil, err
		}
		if len(vals) > 0 {
			return vals[0], nil
		}
		return false, nil

	case plc.ModbusInputRegister:
		return c.readRegisterValue(client, address, source, client.ReadInputRegisters)

	case plc.ModbusHoldingRegister:
		return c.readRegisterValue(client, address, source, client.ReadHoldingRegisters)

	default:
		return nil, fmt.Errorf("unsupported register type: %s", source.RegisterType)
	}
}

func (c *Client) readRegisterValue(
	client *modbusgo.Client,
	address modbusgo.Address,
	source *plc.VariableSource,
	readRegs func(modbusgo.Address, modbusgo.Quantity) ([]uint16, error),
) (interface{}, error) {
	switch source.Format {
	case plc.ModbusFormatInt16:
		regs, err := readRegs(address, 1)
		if err != nil {
			return nil, err
		}
		if len(regs) > 0 {
			return int16(regs[0]), nil
		}
		return int16(0), nil

	case plc.ModbusFormatUint16:
		regs, err := readRegs(address, 1)
		if err != nil {
			return nil, err
		}
		if len(regs) > 0 {
			return regs[0], nil
		}
		return uint16(0), nil

	case plc.ModbusFormatInt32:
		regs, err := readRegs(address, 2)
		if err != nil {
			return nil, err
		}
		val := c.regsToUint32(regs)
		return int32(val), nil

	case plc.ModbusFormatUint32:
		regs, err := readRegs(address, 2)
		if err != nil {
			return nil, err
		}
		return c.regsToUint32(regs), nil

	case plc.ModbusFormatFloat32:
		regs, err := readRegs(address, 2)
		if err != nil {
			return nil, err
		}
		val := c.regsToUint32(regs)
		return math.Float32frombits(val), nil

	case plc.ModbusFormatFloat64:
		regs, err := readRegs(address, 4)
		if err != nil {
			return nil, err
		}
		val := c.regsToUint64(regs)
		return math.Float64frombits(val), nil

	case plc.ModbusFormatBoolean:
		regs, err := readRegs(address, 1)
		if err != nil {
			return nil, err
		}
		if len(regs) > 0 {
			return regs[0] != 0, nil
		}
		return false, nil

	default:
		// Default to uint16
		regs, err := readRegs(address, 1)
		if err != nil {
			return nil, err
		}
		if len(regs) > 0 {
			return regs[0], nil
		}
		return uint16(0), nil
	}
}

// regsToUint32 converts two uint16 registers to uint32.
func (c *Client) regsToUint32(regs []uint16) uint32 {
	if len(regs) < 2 {
		return 0
	}
	// Handle byte/word order based on config
	if c.config.ReverseWords {
		return uint32(regs[1])<<16 | uint32(regs[0])
	}
	return uint32(regs[0])<<16 | uint32(regs[1])
}

// regsToUint64 converts four uint16 registers to uint64.
func (c *Client) regsToUint64(regs []uint16) uint64 {
	if len(regs) < 4 {
		return 0
	}
	if c.config.ReverseWords {
		return uint64(regs[3])<<48 | uint64(regs[2])<<32 | uint64(regs[1])<<16 | uint64(regs[0])
	}
	return uint64(regs[0])<<48 | uint64(regs[1])<<32 | uint64(regs[2])<<16 | uint64(regs[3])
}

// Write writes a value to a Modbus register based on the variable source configuration.
func (c *Client) Write(source *plc.VariableSource, value interface{}) error {
	c.mu.RLock()
	client := c.client
	state := c.state
	c.mu.RUnlock()

	if state != plc.StateConnected || client == nil || !client.IsConnected() {
		return fmt.Errorf("modbus client not connected")
	}

	address := modbusgo.Address(source.Register)

	switch source.RegisterType {
	case plc.ModbusCoil:
		boolVal, ok := value.(bool)
		if !ok {
			// Try to convert from numeric
			switch v := value.(type) {
			case int:
				boolVal = v != 0
			case float64:
				boolVal = v != 0
			default:
				return fmt.Errorf("cannot convert %T to bool", value)
			}
		}
		return client.WriteSingleCoil(address, boolVal)

	case plc.ModbusHoldingRegister:
		return c.writeRegisterValue(client, source, value)

	default:
		return fmt.Errorf("register type %s is read-only", source.RegisterType)
	}
}

func (c *Client) writeRegisterValue(client *modbusgo.Client, source *plc.VariableSource, value interface{}) error {
	address := modbusgo.Address(source.Register)

	switch source.Format {
	case plc.ModbusFormatInt16, plc.ModbusFormatUint16:
		val, err := toUint16(value)
		if err != nil {
			return err
		}
		return client.WriteSingleRegister(address, val)

	case plc.ModbusFormatInt32, plc.ModbusFormatUint32:
		val, err := toUint32(value)
		if err != nil {
			return err
		}
		regs := c.uint32ToRegs(val)
		return client.WriteMultipleRegisters(address, regs)

	case plc.ModbusFormatFloat32:
		val, err := toFloat32(value)
		if err != nil {
			return err
		}
		bits := math.Float32bits(val)
		regs := c.uint32ToRegs(bits)
		return client.WriteMultipleRegisters(address, regs)

	case plc.ModbusFormatFloat64:
		val, err := toFloat64(value)
		if err != nil {
			return err
		}
		bits := math.Float64bits(val)
		regs := c.uint64ToRegs(bits)
		return client.WriteMultipleRegisters(address, regs)

	case plc.ModbusFormatBoolean:
		boolVal, ok := value.(bool)
		if !ok {
			return fmt.Errorf("cannot convert %T to bool", value)
		}
		var val uint16
		if boolVal {
			val = 1
		}
		return client.WriteSingleRegister(address, val)

	default:
		val, err := toUint16(value)
		if err != nil {
			return err
		}
		return client.WriteSingleRegister(address, val)
	}
}

// uint32ToRegs converts uint32 to two uint16 registers.
func (c *Client) uint32ToRegs(val uint32) []uint16 {
	if c.config.ReverseWords {
		return []uint16{uint16(val), uint16(val >> 16)}
	}
	return []uint16{uint16(val >> 16), uint16(val)}
}

// uint64ToRegs converts uint64 to four uint16 registers.
func (c *Client) uint64ToRegs(val uint64) []uint16 {
	if c.config.ReverseWords {
		return []uint16{uint16(val), uint16(val >> 16), uint16(val >> 32), uint16(val >> 48)}
	}
	return []uint16{uint16(val >> 48), uint16(val >> 32), uint16(val >> 16), uint16(val)}
}

// Config returns the client configuration.
func (c *Client) Config() plc.ModbusSourceConfig {
	return c.config
}

// Helper functions for type conversion

func toUint16(v interface{}) (uint16, error) {
	switch val := v.(type) {
	case uint16:
		return val, nil
	case int16:
		return uint16(val), nil
	case int:
		return uint16(val), nil
	case int32:
		return uint16(val), nil
	case int64:
		return uint16(val), nil
	case uint32:
		return uint16(val), nil
	case uint64:
		return uint16(val), nil
	case float32:
		return uint16(val), nil
	case float64:
		return uint16(val), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to uint16", v)
	}
}

func toUint32(v interface{}) (uint32, error) {
	switch val := v.(type) {
	case uint32:
		return val, nil
	case int32:
		return uint32(val), nil
	case int:
		return uint32(val), nil
	case int64:
		return uint32(val), nil
	case uint16:
		return uint32(val), nil
	case uint64:
		return uint32(val), nil
	case float32:
		return uint32(val), nil
	case float64:
		return uint32(val), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to uint32", v)
	}
}

func toFloat32(v interface{}) (float32, error) {
	switch val := v.(type) {
	case float32:
		return val, nil
	case float64:
		return float32(val), nil
	case int:
		return float32(val), nil
	case int32:
		return float32(val), nil
	case int64:
		return float32(val), nil
	case uint16:
		return float32(val), nil
	case uint32:
		return float32(val), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to float32", v)
	}
}

func toFloat64(v interface{}) (float64, error) {
	switch val := v.(type) {
	case float64:
		return val, nil
	case float32:
		return float64(val), nil
	case int:
		return float64(val), nil
	case int32:
		return float64(val), nil
	case int64:
		return float64(val), nil
	case uint16:
		return float64(val), nil
	case uint32:
		return float64(val), nil
	case uint64:
		return float64(val), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", v)
	}
}

// Ensure binary package is used (for potential future use)
var _ = binary.BigEndian
