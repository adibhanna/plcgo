// Package plc provides the core PLC runtime and type definitions.
package plc

import (
	"encoding/json"
	"sync"
	"time"
)

// DataType represents the type of a variable's value.
type DataType string

const (
	DataTypeNumber  DataType = "number"
	DataTypeBoolean DataType = "boolean"
	DataTypeString  DataType = "string"
	DataTypeUDT     DataType = "udt" // User-defined type
)

// SourceType represents the type of data source.
type SourceType string

const (
	SourceTypeModbus SourceType = "modbus"
	SourceTypeOPCUA  SourceType = "opcua"
	SourceTypeMQTT   SourceType = "mqtt"
	SourceTypeRedis  SourceType = "redis"
	SourceTypeREST   SourceType = "rest"
)

// ConnectionState represents the current state of a source connection.
type ConnectionState string

const (
	StateDisconnected ConnectionState = "disconnected"
	StateConnecting   ConnectionState = "connecting"
	StateConnected    ConnectionState = "connected"
	StateError        ConnectionState = "error"
)

// ModbusRegisterType represents the type of Modbus register.
type ModbusRegisterType string

const (
	ModbusHoldingRegister ModbusRegisterType = "HOLDING_REGISTER"
	ModbusInputRegister   ModbusRegisterType = "INPUT_REGISTER"
	ModbusCoil            ModbusRegisterType = "COIL"
	ModbusDiscreteInput   ModbusRegisterType = "DISCRETE_INPUT"
)

// ModbusFormat represents the data format for Modbus registers.
type ModbusFormat string

const (
	ModbusFormatInt16   ModbusFormat = "INT16"
	ModbusFormatUint16  ModbusFormat = "UINT16"
	ModbusFormatInt32   ModbusFormat = "INT32"
	ModbusFormatUint32  ModbusFormat = "UINT32"
	ModbusFormatFloat32 ModbusFormat = "FLOAT32"
	ModbusFormatFloat64 ModbusFormat = "FLOAT64"
	ModbusFormatString  ModbusFormat = "STRING"
	ModbusFormatBoolean ModbusFormat = "BOOLEAN"
)

// ErrorInfo contains information about an error that occurred.
type ErrorInfo struct {
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
	Code      string    `json:"code,omitempty"`
	Stack     string    `json:"stack,omitempty"`
}

// Duration is a wrapper around time.Duration for JSON marshaling.
type Duration time.Duration

// MarshalJSON implements json.Marshaler.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON implements json.Unmarshaler.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch value := v.(type) {
	case string:
		dur, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		*d = Duration(dur)
	case float64:
		*d = Duration(time.Duration(value) * time.Millisecond)
	}
	return nil
}

// Duration returns the underlying time.Duration.
func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

// ModbusSourceConfig extends SourceConfig for Modbus sources.
type ModbusSourceConfig struct {
	SourceConfig
	Host         string   `json:"host"`
	Port         int      `json:"port"`
	UnitID       uint8    `json:"unitId"`
	Timeout      Duration `json:"timeout,omitempty"`
	ReverseBits  bool     `json:"reverseBits,omitempty"`
	ReverseWords bool     `json:"reverseWords,omitempty"`
}

// OPCUASourceConfig extends SourceConfig for OPC UA sources.
type OPCUASourceConfig struct {
	SourceConfig
	Endpoint       string `json:"endpoint"`
	SecurityPolicy string `json:"securityPolicy,omitempty"`
	SecurityMode   string `json:"securityMode,omitempty"`
	Username       string `json:"username,omitempty"`
	Password       string `json:"password,omitempty"`
	CertFile       string `json:"certFile,omitempty"`
	KeyFile        string `json:"keyFile,omitempty"`
}

// MQTTSourceConfig extends SourceConfig for MQTT sources.
type MQTTSourceConfig struct {
	SourceConfig
	ServerURL    string `json:"serverUrl"`
	ClientID     string `json:"clientId,omitempty"`
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	UseTLS       bool   `json:"useTls,omitempty"`
	CACertFile   string `json:"caCertFile,omitempty"`
	CertFile     string `json:"certFile,omitempty"`
	KeyFile      string `json:"keyFile,omitempty"`
	CleanSession bool   `json:"cleanSession,omitempty"`
	// Sparkplug B settings
	GroupID    string `json:"groupId,omitempty"`
	EdgeNodeID string `json:"edgeNodeId,omitempty"`
	DeviceID   string `json:"deviceId,omitempty"`
}

// RedisSourceConfig extends SourceConfig for Redis sources.
type RedisSourceConfig struct {
	SourceConfig
	URL      string `json:"url"`
	Password string `json:"password,omitempty"`
	DB       int    `json:"db,omitempty"`
}

// RESTSourceConfig extends SourceConfig for REST sources.
type RESTSourceConfig struct {
	SourceConfig
	BaseURL string            `json:"baseUrl"`
	Headers map[string]string `json:"headers,omitempty"`
	Timeout Duration          `json:"timeout,omitempty"`
}

// VariableSource defines how a variable reads/writes data from a source.
type VariableSource struct {
	SourceID      string     `json:"sourceId"`
	Type          SourceType `json:"type"`
	Rate          Duration   `json:"rate,omitempty"` // Poll rate
	Bidirectional bool       `json:"bidirectional,omitempty"`

	// Modbus-specific
	Register     uint16             `json:"register,omitempty"`
	RegisterType ModbusRegisterType `json:"registerType,omitempty"`
	Format       ModbusFormat       `json:"format,omitempty"`

	// OPC UA-specific
	NodeID string `json:"nodeId,omitempty"`

	// MQTT-specific
	Topic    string `json:"topic,omitempty"`
	QoS      byte   `json:"qos,omitempty"`
	Retained bool   `json:"retained,omitempty"`

	// Redis-specific
	Key string `json:"key,omitempty"`

	// REST-specific
	Path            string            `json:"path,omitempty"`
	Method          string            `json:"method,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	SetFromResponse bool              `json:"setFromResponse,omitempty"`
}

// VariableConfig defines a PLC variable.
type VariableConfig struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	DataType    DataType        `json:"datatype"`
	Default     interface{}     `json:"default,omitempty"`
	Source      *VariableSource `json:"source,omitempty"`

	// Number-specific settings
	Decimals int     `json:"decimals,omitempty"`
	Deadband float64 `json:"deadband,omitempty"`
	Min      float64 `json:"min,omitempty"`
	Max      float64 `json:"max,omitempty"`

	// Publish rate for external systems (milliseconds)
	PublishRate Duration `json:"publishRate,omitempty"`

	// Whether this variable can be written to
	Writable bool `json:"writable,omitempty"`
}

// VariableValue holds the runtime value of a variable.
type VariableValue struct {
	Value     interface{} `json:"value"`
	Quality   string      `json:"quality"`
	Timestamp time.Time   `json:"timestamp"`
	Error     *ErrorInfo  `json:"error,omitempty"`
}

// Variable represents a runtime variable with its config and current value.
type Variable struct {
	Config VariableConfig `json:"config"`
	Value  VariableValue  `json:"value"`
	mu     sync.RWMutex
}

// NewVariable creates a new Variable from config with default value.
func NewVariable(config VariableConfig) *Variable {
	return &Variable{
		Config: config,
		Value: VariableValue{
			Value:     config.Default,
			Quality:   "good",
			Timestamp: time.Now(),
		},
	}
}

// GetValue returns the current value thread-safely.
func (v *Variable) GetValue() VariableValue {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.Value
}

// SetValue updates the value thread-safely.
func (v *Variable) SetValue(val interface{}, quality string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.Value = VariableValue{
		Value:     val,
		Quality:   quality,
		Timestamp: time.Now(),
		Error:     nil,
	}
}

// SetError sets an error on the variable.
func (v *Variable) SetError(err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.Value.Error = &ErrorInfo{
		Message:   err.Error(),
		Timestamp: time.Now(),
	}
	v.Value.Quality = "bad"
}

// ClearError clears the error on the variable.
func (v *Variable) ClearError() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.Value.Error = nil
	if v.Value.Quality == "bad" {
		v.Value.Quality = "good"
	}
}

// TaskMetrics holds performance metrics for a task.
type TaskMetrics struct {
	WaitStart    time.Time     `json:"waitStart"`
	WaitEnd      time.Time     `json:"waitEnd"`
	WaitDuration time.Duration `json:"waitDuration"`

	ExecuteStart    time.Time     `json:"executeStart"`
	ExecuteEnd      time.Time     `json:"executeEnd"`
	ExecuteDuration time.Duration `json:"executeDuration"`
}

// TaskConfig defines a scheduled task.
type TaskConfig struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	ScanRate    Duration `json:"scanRate"` // Execution interval
	Enabled     bool     `json:"enabled"`
	Priority    int      `json:"priority,omitempty"`
}

// TaskProgram is a function that executes task logic.
type TaskProgram func(ctx TaskContext) error

// TaskContext provides access to variables and update function within a task.
type TaskContext struct {
	Variables map[string]*Variable
	SetValue  func(variableID string, value interface{}) error
}

// TaskRuntime holds runtime information about a task.
type TaskRuntime struct {
	Config         TaskConfig   `json:"config"`
	Program        TaskProgram  `json:"-"`
	Metrics        TaskMetrics  `json:"metrics"`
	ExecutionCount int64        `json:"executionCount"`
	ErrorCount     int64        `json:"errorCount"`
	LastError      *ErrorInfo   `json:"lastError,omitempty"`
	Running        bool         `json:"running"`
	interval       *time.Ticker `json:"-"`
	stopCh         chan struct{}
	mu             sync.RWMutex
}

// TaskRuntimeSnapshot holds a point-in-time snapshot of task runtime data.
type TaskRuntimeSnapshot struct {
	Config         TaskConfig
	Metrics        TaskMetrics
	ExecutionCount int64
	ErrorCount     int64
	LastError      *ErrorInfo
	Running        bool
}

// Snapshot returns a thread-safe snapshot of the task runtime state.
func (t *TaskRuntime) Snapshot() TaskRuntimeSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return TaskRuntimeSnapshot{
		Config:         t.Config,
		Metrics:        t.Metrics,
		ExecutionCount: t.ExecutionCount,
		ErrorCount:     t.ErrorCount,
		LastError:      t.LastError,
		Running:        t.Running,
	}
}

// Config is the main PLC configuration.
type Config struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	// Redis for state persistence (optional)
	RedisURL string `json:"redisUrl,omitempty"`

	// Data sources
	Sources Sources `json:"sources"`

	// Variables
	Variables map[string]VariableConfig `json:"variables"`

	// Tasks (config only, programs added at runtime)
	Tasks map[string]TaskConfig `json:"tasks"`

	// MQTT configuration (for Sparkplug, etc.)
	MQTT map[string]MQTTSourceConfig `json:"mqtt,omitempty"`
}

// Sources holds all configured data sources.
type Sources struct {
	Modbus map[string]ModbusSourceConfig `json:"modbus,omitempty"`
	OPCUA  map[string]OPCUASourceConfig  `json:"opcua,omitempty"`
	MQTT   map[string]MQTTSourceConfig   `json:"mqtt,omitempty"`
	Redis  map[string]RedisSourceConfig  `json:"redis,omitempty"`
	REST   map[string]RESTSourceConfig   `json:"rest,omitempty"`
}

// SourceRuntime holds runtime state for a source connection.
type SourceRuntime struct {
	Config     interface{}     `json:"config"`
	State      ConnectionState `json:"state"`
	Error      *ErrorInfo      `json:"error,omitempty"`
	RetryCount int             `json:"retryCount"`
	Client     interface{}     `json:"-"`
}
