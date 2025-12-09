// Package opcua provides an OPC UA client wrapper with state management for plcgo.
package opcua

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/adibhanna/plcgo/pkg/plc"
	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/ua"
)

// Client wraps the gopcua client with connection state management.
type Client struct {
	config plc.OPCUASourceConfig
	client *opcua.Client
	state  plc.ConnectionState
	error  *plc.ErrorInfo
	mu     sync.RWMutex

	retryCount   int
	retryTimeout *time.Timer
	stopCh       chan struct{}

	// Callbacks
	onConnect    func()
	onDisconnect func()
	onError      func(error)
}

// NewClient creates a new OPC UA client from configuration.
func NewClient(config plc.OPCUASourceConfig) *Client {
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

// Connect establishes the connection to the OPC UA server.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.state == plc.StateConnected && c.client != nil {
		return nil
	}

	c.state = plc.StateConnecting

	// Build client options
	opts := []opcua.Option{
		opcua.SecurityMode(ua.MessageSecurityModeNone),
	}

	// Add security options if configured
	if c.config.SecurityPolicy != "" {
		policy := ua.SecurityPolicyURINone
		switch c.config.SecurityPolicy {
		case "Basic128Rsa15":
			policy = ua.SecurityPolicyURIBasic128Rsa15
		case "Basic256":
			policy = ua.SecurityPolicyURIBasic256
		case "Basic256Sha256":
			policy = ua.SecurityPolicyURIBasic256Sha256
		}
		opts = append(opts, opcua.SecurityPolicy(policy))
	}

	if c.config.SecurityMode != "" {
		mode := ua.MessageSecurityModeNone
		switch c.config.SecurityMode {
		case "Sign":
			mode = ua.MessageSecurityModeSign
		case "SignAndEncrypt":
			mode = ua.MessageSecurityModeSignAndEncrypt
		}
		opts = append(opts, opcua.SecurityMode(mode))
	}

	// Add authentication if configured
	if c.config.Username != "" {
		opts = append(opts, opcua.AuthUsername(c.config.Username, c.config.Password))
	}

	// Add certificates if configured
	if c.config.CertFile != "" && c.config.KeyFile != "" {
		opts = append(opts, opcua.CertificateFile(c.config.CertFile))
		opts = append(opts, opcua.PrivateKeyFile(c.config.KeyFile))
	}

	// Create client
	client, err := opcua.NewClient(c.config.Endpoint, opts...)
	if err != nil {
		c.state = plc.StateError
		c.error = &plc.ErrorInfo{
			Message:   err.Error(),
			Timestamp: time.Now(),
		}
		c.scheduleRetry()
		return err
	}

	// Connect
	if err := client.Connect(ctx); err != nil {
		c.state = plc.StateError
		c.error = &plc.ErrorInfo{
			Message:   err.Error(),
			Timestamp: time.Now(),
		}
		c.scheduleRetry()
		return err
	}

	c.client = client
	c.state = plc.StateConnected
	c.error = nil
	c.retryCount = 0

	if c.onConnect != nil {
		go c.onConnect()
	}

	return nil
}

// Disconnect closes the connection.
func (c *Client) Disconnect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.retryTimeout != nil {
		c.retryTimeout.Stop()
		c.retryTimeout = nil
	}

	if c.client != nil {
		if err := c.client.Close(ctx); err != nil {
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
	return c.state == plc.StateConnected && c.client != nil
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

// Read reads a value from an OPC UA node.
func (c *Client) Read(ctx context.Context, source *plc.VariableSource) (interface{}, error) {
	c.mu.RLock()
	if !c.IsConnected() {
		c.mu.RUnlock()
		return nil, fmt.Errorf("opcua client not connected")
	}
	client := c.client
	c.mu.RUnlock()

	nodeID, err := ua.ParseNodeID(source.NodeID)
	if err != nil {
		return nil, fmt.Errorf("invalid node ID %s: %w", source.NodeID, err)
	}

	req := &ua.ReadRequest{
		MaxAge: 2000,
		NodesToRead: []*ua.ReadValueID{
			{
				NodeID:      nodeID,
				AttributeID: ua.AttributeIDValue,
			},
		},
		TimestampsToReturn: ua.TimestampsToReturnBoth,
	}

	resp, err := client.Read(ctx, req)
	if err != nil {
		return nil, err
	}

	if len(resp.Results) == 0 {
		return nil, fmt.Errorf("no results returned")
	}

	result := resp.Results[0]
	if result.Status != ua.StatusOK {
		return nil, fmt.Errorf("read failed with status: %v", result.Status)
	}

	return result.Value.Value(), nil
}

// Write writes a value to an OPC UA node.
func (c *Client) Write(ctx context.Context, source *plc.VariableSource, value interface{}) error {
	c.mu.RLock()
	if !c.IsConnected() {
		c.mu.RUnlock()
		return fmt.Errorf("opcua client not connected")
	}
	client := c.client
	c.mu.RUnlock()

	nodeID, err := ua.ParseNodeID(source.NodeID)
	if err != nil {
		return fmt.Errorf("invalid node ID %s: %w", source.NodeID, err)
	}

	variant, err := ua.NewVariant(value)
	if err != nil {
		return fmt.Errorf("failed to create variant: %w", err)
	}

	req := &ua.WriteRequest{
		NodesToWrite: []*ua.WriteValue{
			{
				NodeID:      nodeID,
				AttributeID: ua.AttributeIDValue,
				Value: &ua.DataValue{
					EncodingMask: ua.DataValueValue,
					Value:        variant,
				},
			},
		},
	}

	resp, err := client.Write(ctx, req)
	if err != nil {
		return err
	}

	if len(resp.Results) == 0 {
		return fmt.Errorf("no results returned")
	}

	if resp.Results[0] != ua.StatusOK {
		return fmt.Errorf("write failed with status: %v", resp.Results[0])
	}

	return nil
}

// Subscribe subscribes to value changes on an OPC UA node.
func (c *Client) Subscribe(ctx context.Context, source *plc.VariableSource, handler func(interface{})) error {
	c.mu.RLock()
	if !c.IsConnected() {
		c.mu.RUnlock()
		return fmt.Errorf("opcua client not connected")
	}
	client := c.client
	c.mu.RUnlock()

	nodeID, err := ua.ParseNodeID(source.NodeID)
	if err != nil {
		return fmt.Errorf("invalid node ID %s: %w", source.NodeID, err)
	}

	notifyCh := make(chan *opcua.PublishNotificationData)

	sub, err := client.Subscribe(ctx, &opcua.SubscriptionParameters{
		Interval: source.Rate.Duration(),
	}, notifyCh)
	if err != nil {
		return err
	}

	// Create monitored item
	_, err = sub.Monitor(ctx, ua.TimestampsToReturnBoth, &ua.MonitoredItemCreateRequest{
		ItemToMonitor: &ua.ReadValueID{
			NodeID:      nodeID,
			AttributeID: ua.AttributeIDValue,
		},
		MonitoringMode: ua.MonitoringModeReporting,
		RequestedParameters: &ua.MonitoringParameters{
			SamplingInterval: float64(source.Rate.Duration().Milliseconds()),
			QueueSize:        10,
			DiscardOldest:    true,
		},
	})
	if err != nil {
		return err
	}

	// Handle notifications in a goroutine
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.stopCh:
				return
			case res := <-notifyCh:
				if res.Error != nil {
					continue
				}
				switch x := res.Value.(type) {
				case *ua.DataChangeNotification:
					for _, item := range x.MonitoredItems {
						if item.Value != nil && item.Value.Value != nil {
							handler(item.Value.Value.Value())
						}
					}
				}
			}
		}
	}()

	return nil
}

// Config returns the client configuration.
func (c *Client) Config() plc.OPCUASourceConfig {
	return c.config
}
