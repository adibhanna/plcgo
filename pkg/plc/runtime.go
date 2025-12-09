// Package plc provides the core PLC runtime and type definitions.
package plc

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// PLC represents a running PLC instance.
type PLC struct {
	config    Config
	variables map[string]*Variable
	tasks     map[string]*TaskRuntime
	sources   *SourcesRuntime
	mu        sync.RWMutex

	// Event channels
	updateCh chan VariableUpdate
	stopCh   chan struct{}

	// Callbacks
	onVariableUpdate func(variableID string, value interface{})
	onTaskError      func(taskID string, err error)

	logger *slog.Logger
}

// SourcesRuntime holds all runtime source clients.
type SourcesRuntime struct {
	Modbus map[string]interface{} // *modbus.Client
	OPCUA  map[string]interface{} // *opcua.Client
	MQTT   map[string]interface{} // *mqtt.Client
	Redis  map[string]interface{} // *redis.Client
	REST   map[string]interface{} // *rest.Client

	// Polling intervals
	intervals map[string]*time.Ticker
	mu        sync.RWMutex
}

// VariableUpdate represents a variable value update event.
type VariableUpdate struct {
	VariableID string
	Value      interface{}
	Timestamp  time.Time
}

// Option is a functional option for configuring the PLC.
type Option func(*PLC)

// WithLogger sets a custom logger.
func WithLogger(logger *slog.Logger) Option {
	return func(p *PLC) {
		p.logger = logger
	}
}

// WithVariableUpdateCallback sets a callback for variable updates.
func WithVariableUpdateCallback(cb func(variableID string, value interface{})) Option {
	return func(p *PLC) {
		p.onVariableUpdate = cb
	}
}

// WithTaskErrorCallback sets a callback for task errors.
func WithTaskErrorCallback(cb func(taskID string, err error)) Option {
	return func(p *PLC) {
		p.onTaskError = cb
	}
}

// New creates a new PLC instance from configuration.
func New(config Config, opts ...Option) *PLC {
	p := &PLC{
		config:    config,
		variables: make(map[string]*Variable),
		tasks:     make(map[string]*TaskRuntime),
		sources: &SourcesRuntime{
			Modbus:    make(map[string]interface{}),
			OPCUA:     make(map[string]interface{}),
			MQTT:      make(map[string]interface{}),
			Redis:     make(map[string]interface{}),
			REST:      make(map[string]interface{}),
			intervals: make(map[string]*time.Ticker),
		},
		updateCh: make(chan VariableUpdate, 100),
		stopCh:   make(chan struct{}),
		logger:   slog.Default(),
	}

	// Apply options
	for _, opt := range opts {
		opt(p)
	}

	// Initialize variables from config
	for id, varConfig := range config.Variables {
		varConfig.ID = id
		p.variables[id] = NewVariable(varConfig)
	}

	// Initialize task runtimes from config
	for id, taskConfig := range config.Tasks {
		taskConfig.ID = id
		p.tasks[id] = &TaskRuntime{
			Config: taskConfig,
			stopCh: make(chan struct{}),
		}
	}

	return p
}

// RegisterTask registers a task program.
func (p *PLC) RegisterTask(taskID string, program TaskProgram) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	task, exists := p.tasks[taskID]
	if !exists {
		return fmt.Errorf("task %s not found", taskID)
	}

	task.Program = program
	return nil
}

// RegisterModbusClient registers a Modbus client for a source.
func (p *PLC) RegisterModbusClient(sourceID string, client interface{}) {
	p.sources.mu.Lock()
	defer p.sources.mu.Unlock()
	p.sources.Modbus[sourceID] = client
}

// RegisterOPCUAClient registers an OPC UA client for a source.
func (p *PLC) RegisterOPCUAClient(sourceID string, client interface{}) {
	p.sources.mu.Lock()
	defer p.sources.mu.Unlock()
	p.sources.OPCUA[sourceID] = client
}

// RegisterMQTTClient registers an MQTT client for a source.
func (p *PLC) RegisterMQTTClient(sourceID string, client interface{}) {
	p.sources.mu.Lock()
	defer p.sources.mu.Unlock()
	p.sources.MQTT[sourceID] = client
}

// RegisterRedisClient registers a Redis client for a source.
func (p *PLC) RegisterRedisClient(sourceID string, client interface{}) {
	p.sources.mu.Lock()
	defer p.sources.mu.Unlock()
	p.sources.Redis[sourceID] = client
}

// RegisterRESTClient registers a REST client for a source.
func (p *PLC) RegisterRESTClient(sourceID string, client interface{}) {
	p.sources.mu.Lock()
	defer p.sources.mu.Unlock()
	p.sources.REST[sourceID] = client
}

// Start starts the PLC runtime.
func (p *PLC) Start(ctx context.Context) error {
	p.logger.Info("Starting PLC", "id", p.config.ID, "name", p.config.Name)

	// Start update handler
	go p.handleUpdates(ctx)

	// Start enabled tasks
	for id, task := range p.tasks {
		if task.Config.Enabled && task.Program != nil {
			if err := p.startTask(ctx, id); err != nil {
				p.logger.Error("Failed to start task", "task", id, "error", err)
			}
		}
	}

	p.logger.Info("PLC started successfully")
	return nil
}

// Stop stops the PLC runtime.
func (p *PLC) Stop() error {
	p.logger.Info("Stopping PLC", "id", p.config.ID)

	close(p.stopCh)

	// Stop all tasks
	for id := range p.tasks {
		p.stopTask(id)
	}

	// Stop all source intervals
	p.sources.mu.Lock()
	for _, ticker := range p.sources.intervals {
		ticker.Stop()
	}
	p.sources.mu.Unlock()

	p.logger.Info("PLC stopped")
	return nil
}

func (p *PLC) handleUpdates(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case update := <-p.updateCh:
			if p.onVariableUpdate != nil {
				p.onVariableUpdate(update.VariableID, update.Value)
			}
		}
	}
}

func (p *PLC) startTask(ctx context.Context, taskID string) error {
	p.mu.Lock()
	task, exists := p.tasks[taskID]
	if !exists {
		p.mu.Unlock()
		return fmt.Errorf("task %s not found", taskID)
	}

	if task.Running {
		p.mu.Unlock()
		return fmt.Errorf("task %s is already running", taskID)
	}

	task.Running = true
	task.stopCh = make(chan struct{})
	p.mu.Unlock()

	go p.runTask(ctx, task)
	p.logger.Info("Started task", "task", taskID, "scanRate", task.Config.ScanRate.Duration())

	return nil
}

func (p *PLC) stopTask(taskID string) {
	p.mu.Lock()
	task, exists := p.tasks[taskID]
	if !exists || !task.Running {
		p.mu.Unlock()
		return
	}

	close(task.stopCh)
	task.Running = false
	if task.interval != nil {
		task.interval.Stop()
	}
	p.mu.Unlock()

	p.logger.Info("Stopped task", "task", taskID)
}

func (p *PLC) runTask(ctx context.Context, task *TaskRuntime) {
	scanRate := task.Config.ScanRate.Duration()
	if scanRate == 0 {
		scanRate = time.Second
	}

	task.interval = time.NewTicker(scanRate)
	defer task.interval.Stop()

	task.mu.Lock()
	task.Metrics.WaitStart = time.Now()
	task.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return
		case <-task.stopCh:
			return
		case <-task.interval.C:
			p.executeTask(ctx, task)
		}
	}
}

func (p *PLC) executeTask(ctx context.Context, task *TaskRuntime) {
	task.mu.Lock()
	task.Metrics.WaitEnd = time.Now()
	task.Metrics.WaitDuration = task.Metrics.WaitEnd.Sub(task.Metrics.WaitStart)
	task.Metrics.ExecuteStart = time.Now()
	task.mu.Unlock()

	// Create task context
	taskCtx := TaskContext{
		Variables: p.variables,
		SetValue:  p.SetValue,
	}

	// Execute program
	err := task.Program(taskCtx)

	task.mu.Lock()
	task.Metrics.ExecuteEnd = time.Now()
	task.Metrics.ExecuteDuration = task.Metrics.ExecuteEnd.Sub(task.Metrics.ExecuteStart)
	task.ExecutionCount++

	if err != nil {
		task.ErrorCount++
		task.LastError = &ErrorInfo{
			Message:   err.Error(),
			Timestamp: time.Now(),
		}
		if p.onTaskError != nil {
			go p.onTaskError(task.Config.ID, err)
		}
		p.logger.Error("Task execution error", "task", task.Config.ID, "error", err)
	} else {
		task.LastError = nil
	}

	task.Metrics.WaitStart = time.Now()
	task.mu.Unlock()
}

// SetValue sets a variable's value.
func (p *PLC) SetValue(variableID string, value interface{}) error {
	p.mu.RLock()
	variable, exists := p.variables[variableID]
	p.mu.RUnlock()

	if !exists {
		return fmt.Errorf("variable %s not found", variableID)
	}

	variable.SetValue(value, "good")

	// Send update notification
	select {
	case p.updateCh <- VariableUpdate{
		VariableID: variableID,
		Value:      value,
		Timestamp:  time.Now(),
	}:
	default:
		// Channel full, skip
	}

	return nil
}

// GetValue gets a variable's current value.
func (p *PLC) GetValue(variableID string) (interface{}, error) {
	p.mu.RLock()
	variable, exists := p.variables[variableID]
	p.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("variable %s not found", variableID)
	}

	return variable.GetValue().Value, nil
}

// GetVariable returns a variable by ID.
func (p *PLC) GetVariable(variableID string) (*Variable, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, exists := p.variables[variableID]
	return v, exists
}

// GetVariables returns all variables.
func (p *PLC) GetVariables() map[string]*Variable {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Return a copy
	vars := make(map[string]*Variable, len(p.variables))
	for k, v := range p.variables {
		vars[k] = v
	}
	return vars
}

// GetTask returns a task by ID.
func (p *PLC) GetTask(taskID string) (*TaskRuntime, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	t, exists := p.tasks[taskID]
	return t, exists
}

// GetTasks returns all tasks.
func (p *PLC) GetTasks() map[string]*TaskRuntime {
	p.mu.RLock()
	defer p.mu.RUnlock()

	tasks := make(map[string]*TaskRuntime, len(p.tasks))
	for k, v := range p.tasks {
		tasks[k] = v
	}
	return tasks
}

// GetConfig returns the PLC configuration.
func (p *PLC) GetConfig() Config {
	return p.config
}

// GetSources returns the sources runtime.
func (p *PLC) GetSources() *SourcesRuntime {
	return p.sources
}

// GetModbusClient returns a Modbus client by source ID.
func (p *PLC) GetModbusClient(sourceID string) (interface{}, bool) {
	p.sources.mu.RLock()
	defer p.sources.mu.RUnlock()
	client, exists := p.sources.Modbus[sourceID]
	return client, exists
}

// GetOPCUAClient returns an OPC UA client by source ID.
func (p *PLC) GetOPCUAClient(sourceID string) (interface{}, bool) {
	p.sources.mu.RLock()
	defer p.sources.mu.RUnlock()
	client, exists := p.sources.OPCUA[sourceID]
	return client, exists
}

// GetMQTTClient returns an MQTT client by source ID.
func (p *PLC) GetMQTTClient(sourceID string) (interface{}, bool) {
	p.sources.mu.RLock()
	defer p.sources.mu.RUnlock()
	client, exists := p.sources.MQTT[sourceID]
	return client, exists
}

// GetRedisClient returns a Redis client by source ID.
func (p *PLC) GetRedisClient(sourceID string) (interface{}, bool) {
	p.sources.mu.RLock()
	defer p.sources.mu.RUnlock()
	client, exists := p.sources.Redis[sourceID]
	return client, exists
}

// GetRESTClient returns a REST client by source ID.
func (p *PLC) GetRESTClient(sourceID string) (interface{}, bool) {
	p.sources.mu.RLock()
	defer p.sources.mu.RUnlock()
	client, exists := p.sources.REST[sourceID]
	return client, exists
}

// ToJSON returns the PLC state as JSON.
func (p *PLC) ToJSON() ([]byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	state := struct {
		Config    Config                    `json:"config"`
		Variables map[string]VariableValue  `json:"variables"`
		Tasks     map[string]taskStateJSON  `json:"tasks"`
	}{
		Config:    p.config,
		Variables: make(map[string]VariableValue),
		Tasks:     make(map[string]taskStateJSON),
	}

	for id, v := range p.variables {
		state.Variables[id] = v.GetValue()
	}

	for id, t := range p.tasks {
		t.mu.RLock()
		state.Tasks[id] = taskStateJSON{
			Config:         t.Config,
			Metrics:        t.Metrics,
			ExecutionCount: t.ExecutionCount,
			ErrorCount:     t.ErrorCount,
			LastError:      t.LastError,
			Running:        t.Running,
		}
		t.mu.RUnlock()
	}

	return json.Marshal(state)
}

type taskStateJSON struct {
	Config         TaskConfig  `json:"config"`
	Metrics        TaskMetrics `json:"metrics"`
	ExecutionCount int64       `json:"executionCount"`
	ErrorCount     int64       `json:"errorCount"`
	LastError      *ErrorInfo  `json:"lastError,omitempty"`
	Running        bool        `json:"running"`
}
