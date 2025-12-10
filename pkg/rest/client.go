// Package rest provides a REST client for plcgo.
package rest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/adibhanna/plcgo/pkg/plc"
)

// Client wraps an HTTP client for REST API access.
type Client struct {
	config     plc.RESTSourceConfig
	httpClient *http.Client
}

// NewClient creates a new REST client from configuration.
func NewClient(config plc.RESTSourceConfig) *Client {
	timeout := config.Timeout.Duration()
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &Client{
		config: config,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Request performs an HTTP request.
func (c *Client) Request(ctx context.Context, method, path string, body []byte, headers map[string]string) ([]byte, int, error) {
	url := c.config.BaseURL + path

	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	// Add default headers
	for k, v := range c.config.Headers {
		req.Header.Set(k, v)
	}

	// Add request-specific headers
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	// Set content-type if body is present
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response: %w", err)
	}

	return respBody, resp.StatusCode, nil
}

// Get performs a GET request.
func (c *Client) Get(ctx context.Context, path string, headers map[string]string) ([]byte, int, error) {
	return c.Request(ctx, http.MethodGet, path, nil, headers)
}

// Post performs a POST request.
func (c *Client) Post(ctx context.Context, path string, body []byte, headers map[string]string) ([]byte, int, error) {
	return c.Request(ctx, http.MethodPost, path, body, headers)
}

// Put performs a PUT request.
func (c *Client) Put(ctx context.Context, path string, body []byte, headers map[string]string) ([]byte, int, error) {
	return c.Request(ctx, http.MethodPut, path, body, headers)
}

// Delete performs a DELETE request.
func (c *Client) Delete(ctx context.Context, path string, headers map[string]string) ([]byte, int, error) {
	return c.Request(ctx, http.MethodDelete, path, nil, headers)
}

// Read reads a value from a REST endpoint based on the variable source configuration.
func (c *Client) Read(ctx context.Context, source *plc.VariableSource) ([]byte, error) {
	method := source.Method
	if method == "" {
		method = http.MethodGet
	}

	var body []byte
	if source.Body != "" {
		body = []byte(source.Body)
	}

	respBody, statusCode, err := c.Request(ctx, method, source.Path, body, source.Headers)
	if err != nil {
		return nil, err
	}

	if statusCode < 200 || statusCode >= 300 {
		return nil, fmt.Errorf("request failed with status %d: %s", statusCode, string(respBody))
	}

	return respBody, nil
}

// Write writes a value to a REST endpoint based on the variable source configuration.
func (c *Client) Write(ctx context.Context, source *plc.VariableSource, value []byte) error {
	method := source.Method
	if method == "" {
		method = http.MethodPost
	}

	_, statusCode, err := c.Request(ctx, method, source.Path, value, source.Headers)
	if err != nil {
		return err
	}

	if statusCode < 200 || statusCode >= 300 {
		return fmt.Errorf("request failed with status %d", statusCode)
	}

	return nil
}

// Config returns the client configuration.
func (c *Client) Config() plc.RESTSourceConfig {
	return c.config
}
