// Package mqtt provides an MQTT client wrapper with Sparkplug B support for plcgo.
package mqtt

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/adibhanna/plcgo/pkg/plc"
)

// SparkplugMetric represents a Sparkplug B metric.
type SparkplugMetric struct {
	Name      string      `json:"name"`
	Timestamp int64       `json:"timestamp"`
	DataType  string      `json:"dataType"`
	Value     interface{} `json:"value"`
}

// SparkplugPayload represents a Sparkplug B payload.
type SparkplugPayload struct {
	Timestamp int64             `json:"timestamp"`
	Metrics   []SparkplugMetric `json:"metrics"`
	Seq       uint64            `json:"seq"`
}

// Client wraps the paho MQTT client with connection state management and Sparkplug support.
type Client struct {
	config plc.MQTTSourceConfig
	client pahomqtt.Client
	state  plc.ConnectionState
	error  *plc.ErrorInfo
	mu     sync.RWMutex

	seq          uint64
	retryCount   int
	retryTimeout *time.Timer
	stopCh       chan struct{}

	// Subscription handlers
	handlers map[string][]func(topic string, payload []byte)

	// Callbacks
	onConnect    func()
	onDisconnect func()
	onError      func(error)
}

// NewClient creates a new MQTT client from configuration.
func NewClient(config plc.MQTTSourceConfig) *Client {
	return &Client{
		config:   config,
		state:    plc.StateDisconnected,
		stopCh:   make(chan struct{}),
		handlers: make(map[string][]func(topic string, payload []byte)),
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

// Connect establishes the connection to the MQTT broker.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.state == plc.StateConnected && c.client != nil && c.client.IsConnected() {
		return nil
	}

	c.state = plc.StateConnecting

	// Build client options
	opts := pahomqtt.NewClientOptions()
	opts.AddBroker(c.config.ServerURL)

	if c.config.ClientID != "" {
		opts.SetClientID(c.config.ClientID)
	}

	if c.config.Username != "" {
		opts.SetUsername(c.config.Username)
		opts.SetPassword(c.config.Password)
	}

	opts.SetCleanSession(c.config.CleanSession)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)

	// TLS configuration
	if c.config.UseTLS {
		tlsConfig, err := c.buildTLSConfig()
		if err != nil {
			c.state = plc.StateError
			c.error = &plc.ErrorInfo{
				Message:   err.Error(),
				Timestamp: time.Now(),
			}
			return err
		}
		opts.SetTLSConfig(tlsConfig)
	}

	// Connection callbacks
	opts.SetOnConnectHandler(func(client pahomqtt.Client) {
		c.mu.Lock()
		c.state = plc.StateConnected
		c.error = nil
		c.retryCount = 0
		callback := c.onConnect
		c.mu.Unlock()

		// Publish Sparkplug NBIRTH if enabled
		if c.config.GroupID != "" && c.config.EdgeNodeID != "" {
			c.publishNBirth()
		}

		if callback != nil {
			go callback()
		}
	})

	opts.SetConnectionLostHandler(func(client pahomqtt.Client, err error) {
		c.mu.Lock()
		c.state = plc.StateDisconnected
		c.error = &plc.ErrorInfo{
			Message:   err.Error(),
			Timestamp: time.Now(),
		}
		callback := c.onDisconnect
		c.mu.Unlock()

		if callback != nil {
			go callback()
		}
	})

	// Set Sparkplug Last Will if enabled
	if c.config.GroupID != "" && c.config.EdgeNodeID != "" {
		willTopic := fmt.Sprintf("spBv1.0/%s/NDEATH/%s", c.config.GroupID, c.config.EdgeNodeID)
		willPayload := c.buildNDeathPayload()
		opts.SetWill(willTopic, string(willPayload), 1, true)
	}

	// Create and connect client
	c.client = pahomqtt.NewClient(opts)

	token := c.client.Connect()
	if !token.WaitTimeout(30 * time.Second) {
		c.state = plc.StateError
		c.error = &plc.ErrorInfo{
			Message:   "connection timeout",
			Timestamp: time.Now(),
		}
		c.scheduleRetry()
		return fmt.Errorf("connection timeout")
	}

	if err := token.Error(); err != nil {
		c.state = plc.StateError
		c.error = &plc.ErrorInfo{
			Message:   err.Error(),
			Timestamp: time.Now(),
		}
		c.scheduleRetry()
		return err
	}

	return nil
}

func (c *Client) buildTLSConfig() (*tls.Config, error) {
	tlsConfig := &tls.Config{}

	if c.config.CACertFile != "" {
		caCert, err := os.ReadFile(c.config.CACertFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA cert: %w", err)
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
		tlsConfig.RootCAs = caCertPool
	}

	if c.config.CertFile != "" && c.config.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(c.config.CertFile, c.config.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client cert: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return tlsConfig, nil
}

// Disconnect closes the connection.
func (c *Client) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.retryTimeout != nil {
		c.retryTimeout.Stop()
		c.retryTimeout = nil
	}

	// Publish Sparkplug NDEATH before disconnecting
	if c.config.GroupID != "" && c.config.EdgeNodeID != "" && c.client != nil && c.client.IsConnected() {
		c.publishNDeath()
	}

	if c.client != nil {
		c.client.Disconnect(1000)
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

// Subscribe subscribes to a topic.
func (c *Client) Subscribe(topic string, qos byte, handler func(topic string, payload []byte)) error {
	c.mu.Lock()
	c.handlers[topic] = append(c.handlers[topic], handler)
	client := c.client
	c.mu.Unlock()

	if client == nil || !client.IsConnected() {
		return fmt.Errorf("mqtt client not connected")
	}

	token := client.Subscribe(topic, qos, func(client pahomqtt.Client, msg pahomqtt.Message) {
		c.mu.RLock()
		handlers := c.handlers[msg.Topic()]
		c.mu.RUnlock()

		for _, h := range handlers {
			h(msg.Topic(), msg.Payload())
		}
	})

	if !token.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("subscribe timeout")
	}

	return token.Error()
}

// Unsubscribe unsubscribes from a topic.
func (c *Client) Unsubscribe(topic string) error {
	c.mu.Lock()
	delete(c.handlers, topic)
	client := c.client
	c.mu.Unlock()

	if client == nil || !client.IsConnected() {
		return nil
	}

	token := client.Unsubscribe(topic)
	token.WaitTimeout(10 * time.Second)
	return token.Error()
}

// Publish publishes a message to a topic.
func (c *Client) Publish(topic string, qos byte, retained bool, payload []byte) error {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if client == nil || !client.IsConnected() {
		return fmt.Errorf("mqtt client not connected")
	}

	token := client.Publish(topic, qos, retained, payload)
	if !token.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("publish timeout")
	}

	return token.Error()
}

// Sparkplug B methods

func (c *Client) buildNDeathPayload() []byte {
	payload := SparkplugPayload{
		Timestamp: time.Now().UnixMilli(),
	}
	data, _ := json.Marshal(payload)
	return data
}

func (c *Client) publishNBirth() {
	topic := fmt.Sprintf("spBv1.0/%s/NBIRTH/%s", c.config.GroupID, c.config.EdgeNodeID)
	payload := SparkplugPayload{
		Timestamp: time.Now().UnixMilli(),
		Seq:       0,
		Metrics:   []SparkplugMetric{},
	}
	c.seq = 0
	data, _ := json.Marshal(payload)
	c.client.Publish(topic, 1, false, data)
}

func (c *Client) publishNDeath() {
	topic := fmt.Sprintf("spBv1.0/%s/NDEATH/%s", c.config.GroupID, c.config.EdgeNodeID)
	payload := c.buildNDeathPayload()
	c.client.Publish(topic, 1, false, payload)
}

// PublishDBirth publishes a Sparkplug DBIRTH message.
func (c *Client) PublishDBirth(deviceID string, metrics []SparkplugMetric) error {
	if c.config.GroupID == "" || c.config.EdgeNodeID == "" {
		return fmt.Errorf("sparkplug not configured")
	}

	topic := fmt.Sprintf("spBv1.0/%s/DBIRTH/%s/%s", c.config.GroupID, c.config.EdgeNodeID, deviceID)
	c.seq++
	payload := SparkplugPayload{
		Timestamp: time.Now().UnixMilli(),
		Seq:       c.seq,
		Metrics:   metrics,
	}
	data, _ := json.Marshal(payload)
	return c.Publish(topic, 1, false, data)
}

// PublishDData publishes a Sparkplug DDATA message.
func (c *Client) PublishDData(deviceID string, metrics []SparkplugMetric) error {
	if c.config.GroupID == "" || c.config.EdgeNodeID == "" {
		return fmt.Errorf("sparkplug not configured")
	}

	topic := fmt.Sprintf("spBv1.0/%s/DDATA/%s/%s", c.config.GroupID, c.config.EdgeNodeID, deviceID)
	c.seq++
	payload := SparkplugPayload{
		Timestamp: time.Now().UnixMilli(),
		Seq:       c.seq,
		Metrics:   metrics,
	}
	data, _ := json.Marshal(payload)
	return c.Publish(topic, 1, false, data)
}

// PublishNData publishes a Sparkplug NDATA message.
func (c *Client) PublishNData(metrics []SparkplugMetric) error {
	if c.config.GroupID == "" || c.config.EdgeNodeID == "" {
		return fmt.Errorf("sparkplug not configured")
	}

	topic := fmt.Sprintf("spBv1.0/%s/NDATA/%s", c.config.GroupID, c.config.EdgeNodeID)
	c.seq++
	payload := SparkplugPayload{
		Timestamp: time.Now().UnixMilli(),
		Seq:       c.seq,
		Metrics:   metrics,
	}
	data, _ := json.Marshal(payload)
	return c.Publish(topic, 1, false, data)
}

// SubscribeDCMD subscribes to Sparkplug DCMD messages.
func (c *Client) SubscribeDCMD(deviceID string, handler func(metrics []SparkplugMetric)) error {
	if c.config.GroupID == "" || c.config.EdgeNodeID == "" {
		return fmt.Errorf("sparkplug not configured")
	}

	topic := fmt.Sprintf("spBv1.0/%s/DCMD/%s/%s", c.config.GroupID, c.config.EdgeNodeID, deviceID)
	return c.Subscribe(topic, 1, func(topic string, payload []byte) {
		var sparkPayload SparkplugPayload
		if err := json.Unmarshal(payload, &sparkPayload); err != nil {
			return
		}
		handler(sparkPayload.Metrics)
	})
}

// SubscribeNCMD subscribes to Sparkplug NCMD messages.
func (c *Client) SubscribeNCMD(handler func(metrics []SparkplugMetric)) error {
	if c.config.GroupID == "" || c.config.EdgeNodeID == "" {
		return fmt.Errorf("sparkplug not configured")
	}

	topic := fmt.Sprintf("spBv1.0/%s/NCMD/%s", c.config.GroupID, c.config.EdgeNodeID)
	return c.Subscribe(topic, 1, func(topic string, payload []byte) {
		var sparkPayload SparkplugPayload
		if err := json.Unmarshal(payload, &sparkPayload); err != nil {
			return
		}
		handler(sparkPayload.Metrics)
	})
}

// Config returns the client configuration.
func (c *Client) Config() plc.MQTTSourceConfig {
	return c.config
}
