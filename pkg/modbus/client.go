// Package modbus provides a Modbus client wrapper with state management for plcgo.
package modbus

import (
	"context"
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
	c.client.SetAutoReconnect(true)

	if c.config.Timeout.Duration() > 0 {
		c.client.SetTimeout(c.config.Timeout.Duration())
	}

	// Configure encoding based on config
	byteOrder := modbusgo.BigEndian
	wordOrder := modbusgo.HighWordFirst
	if c.config.ReverseBits {
		byteOrder = modbusgo.LittleEndian
	}
	if c.config.ReverseWords {
		wordOrder = modbusgo.LowWordFirst
	}
	c.client.SetEncoding(byteOrder, wordOrder)

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
	if !c.IsConnected() {
		c.mu.RUnlock()
		return nil, fmt.Errorf("modbus client not connected")
	}
	client := c.client
	c.mu.RUnlock()

	address := modbusgo.Address(source.Register)

	switch source.RegisterType {
	case plc.ModbusCoil:
		val, err := client.ReadCoil(address)
		if err != nil {
			return nil, err
		}
		return val, nil

	case plc.ModbusDiscreteInput:
		val, err := client.ReadDiscreteInput(address)
		if err != nil {
			return nil, err
		}
		return val, nil

	case plc.ModbusInputRegister:
		return c.readRegisterValue(client.ReadInputRegister, client.ReadInputUint32, client.ReadInputFloat32, source)

	case plc.ModbusHoldingRegister:
		return c.readRegisterValue(client.ReadHoldingRegister, client.ReadUint32, client.ReadFloat32, source)

	default:
		return nil, fmt.Errorf("unsupported register type: %s", source.RegisterType)
	}
}

func (c *Client) readRegisterValue(
	readReg func(modbusgo.Address) (uint16, error),
	readUint32 func(modbusgo.Address) (uint32, error),
	readFloat32 func(modbusgo.Address) (float32, error),
	source *plc.VariableSource,
) (interface{}, error) {
	address := modbusgo.Address(source.Register)

	switch source.Format {
	case plc.ModbusFormatInt16:
		val, err := readReg(address)
		if err != nil {
			return nil, err
		}
		return int16(val), nil

	case plc.ModbusFormatUint16:
		val, err := readReg(address)
		if err != nil {
			return nil, err
		}
		return val, nil

	case plc.ModbusFormatInt32:
		val, err := readUint32(address)
		if err != nil {
			return nil, err
		}
		return int32(val), nil

	case plc.ModbusFormatUint32:
		val, err := readUint32(address)
		if err != nil {
			return nil, err
		}
		return val, nil

	case plc.ModbusFormatFloat32:
		val, err := readFloat32(address)
		if err != nil {
			return nil, err
		}
		return val, nil

	case plc.ModbusFormatFloat64:
		val, err := c.client.ReadFloat64(address)
		if err != nil {
			return nil, err
		}
		return val, nil

	case plc.ModbusFormatBoolean:
		val, err := readReg(address)
		if err != nil {
			return nil, err
		}
		return val != 0, nil

	default:
		// Default to uint16
		val, err := readReg(address)
		if err != nil {
			return nil, err
		}
		return val, nil
	}
}

// Write writes a value to a Modbus register based on the variable source configuration.
func (c *Client) Write(source *plc.VariableSource, value interface{}) error {
	c.mu.RLock()
	if !c.IsConnected() {
		c.mu.RUnlock()
		return fmt.Errorf("modbus client not connected")
	}
	client := c.client
	c.mu.RUnlock()

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
		return client.WriteUint32(address, val)

	case plc.ModbusFormatFloat32:
		val, err := toFloat32(value)
		if err != nil {
			return err
		}
		return client.WriteFloat32(address, val)

	case plc.ModbusFormatFloat64:
		val, err := toFloat64(value)
		if err != nil {
			return err
		}
		return client.WriteFloat64(address, val)

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
