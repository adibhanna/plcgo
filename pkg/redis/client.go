// Package redis provides a Redis client wrapper with pub/sub support for plcgo.
package redis

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/adibhanna/plcgo/pkg/plc"
	goredis "github.com/redis/go-redis/v9"
)

// Client wraps the go-redis client with connection state management.
type Client struct {
	config    plc.RedisSourceConfig
	client    *goredis.Client
	pubsub    *goredis.PubSub
	state     plc.ConnectionState
	error     *plc.ErrorInfo
	mu        sync.RWMutex

	retryCount   int
	retryTimeout *time.Timer
	stopCh       chan struct{}

	// Subscription handlers
	keyHandlers map[string][]func(key string, value string)

	// Callbacks
	onConnect    func()
	onDisconnect func()
	onError      func(error)
}

// NewClient creates a new Redis client from configuration.
func NewClient(config plc.RedisSourceConfig) *Client {
	return &Client{
		config:      config,
		state:       plc.StateDisconnected,
		stopCh:      make(chan struct{}),
		keyHandlers: make(map[string][]func(key string, value string)),
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

// Connect establishes the connection to the Redis server.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.state == plc.StateConnected && c.client != nil {
		return nil
	}

	c.state = plc.StateConnecting

	// Parse URL and create options
	opts, err := goredis.ParseURL(c.config.URL)
	if err != nil {
		c.state = plc.StateError
		c.error = &plc.ErrorInfo{
			Message:   err.Error(),
			Timestamp: time.Now(),
		}
		c.scheduleRetry()
		return err
	}

	if c.config.Password != "" {
		opts.Password = c.config.Password
	}
	if c.config.DB != 0 {
		opts.DB = c.config.DB
	}

	// Create client
	c.client = goredis.NewClient(opts)

	// Test connection
	if err := c.client.Ping(ctx).Err(); err != nil {
		c.state = plc.StateError
		c.error = &plc.ErrorInfo{
			Message:   err.Error(),
			Timestamp: time.Now(),
		}
		c.scheduleRetry()
		return err
	}

	// Enable keyspace notifications
	if err := c.client.ConfigSet(ctx, "notify-keyspace-events", "KEA").Err(); err != nil {
		// This may fail if the user doesn't have permission, but we continue
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

	close(c.stopCh)

	if c.pubsub != nil {
		c.pubsub.Close()
		c.pubsub = nil
	}

	if c.client != nil {
		if err := c.client.Close(); err != nil {
			return err
		}
	}

	c.state = plc.StateDisconnected
	c.client = nil
	c.stopCh = make(chan struct{})

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

// Get retrieves a value from Redis.
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	c.mu.RLock()
	if !c.IsConnected() {
		c.mu.RUnlock()
		return "", fmt.Errorf("redis client not connected")
	}
	client := c.client
	c.mu.RUnlock()

	return client.Get(ctx, key).Result()
}

// Set stores a value in Redis.
func (c *Client) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	c.mu.RLock()
	if !c.IsConnected() {
		c.mu.RUnlock()
		return fmt.Errorf("redis client not connected")
	}
	client := c.client
	c.mu.RUnlock()

	return client.Set(ctx, key, value, expiration).Err()
}

// Publish publishes a message to a channel.
func (c *Client) Publish(ctx context.Context, channel string, message interface{}) error {
	c.mu.RLock()
	if !c.IsConnected() {
		c.mu.RUnlock()
		return fmt.Errorf("redis client not connected")
	}
	client := c.client
	c.mu.RUnlock()

	return client.Publish(ctx, channel, message).Err()
}

// Subscribe subscribes to channels.
func (c *Client) Subscribe(ctx context.Context, channels ...string) (*goredis.PubSub, error) {
	c.mu.RLock()
	if !c.IsConnected() {
		c.mu.RUnlock()
		return nil, fmt.Errorf("redis client not connected")
	}
	client := c.client
	c.mu.RUnlock()

	return client.Subscribe(ctx, channels...), nil
}

// WatchKey watches for changes to a key and calls the handler when it changes.
func (c *Client) WatchKey(ctx context.Context, key string, handler func(key string, value string)) error {
	c.mu.Lock()
	c.keyHandlers[key] = append(c.keyHandlers[key], handler)

	// Start keyspace notification listener if not already started
	if c.pubsub == nil && c.client != nil {
		c.pubsub = c.client.PSubscribe(ctx, "__keyspace@*__:*")
		go c.handleKeyspaceNotifications(ctx)
	}
	c.mu.Unlock()

	return nil
}

func (c *Client) handleKeyspaceNotifications(ctx context.Context) {
	ch := c.pubsub.Channel()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case msg := <-ch:
			if msg == nil {
				continue
			}

			// Extract key from channel name: __keyspace@0__:key
			key := ""
			if len(msg.Channel) > 15 {
				key = msg.Channel[15:] // Skip "__keyspace@0__:"
			}

			if key == "" {
				continue
			}

			c.mu.RLock()
			handlers := c.keyHandlers[key]
			client := c.client
			c.mu.RUnlock()

			if len(handlers) > 0 && client != nil {
				// Get the current value
				value, err := client.Get(ctx, key).Result()
				if err != nil {
					continue
				}

				for _, h := range handlers {
					go h(key, value)
				}
			}
		}
	}
}

// SetAndPublish sets a value and publishes a notification.
func (c *Client) SetAndPublish(ctx context.Context, key string, value interface{}) error {
	if err := c.Set(ctx, key, value, 0); err != nil {
		return err
	}
	return c.Publish(ctx, key, value)
}

// GetClient returns the underlying Redis client.
func (c *Client) GetClient() *goredis.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.client
}

// Config returns the client configuration.
func (c *Client) Config() plc.RedisSourceConfig {
	return c.config
}
