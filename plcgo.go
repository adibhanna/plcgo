// Package plcgo provides a modern software PLC implementation in Go.
// It supports multiple industrial protocols including Modbus TCP/IP, OPC UA,
// MQTT with Sparkplug B, Redis, and REST APIs.
//
// PLCGo is inspired by and compatible with the  PLC architecture,
// providing a Go-native implementation with GraphQL API support.
package plcgo

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/adibhanna/plcgo/pkg/graphql"
	"github.com/adibhanna/plcgo/pkg/modbus"
	"github.com/adibhanna/plcgo/pkg/mqtt"
	"github.com/adibhanna/plcgo/pkg/opcua"
	"github.com/adibhanna/plcgo/pkg/plc"
	"github.com/adibhanna/plcgo/pkg/redis"
	"github.com/adibhanna/plcgo/pkg/rest"
)

// PLCGo wraps the PLC runtime with all protocol clients and the GraphQL server.
type PLCGo struct {
	PLC           *plc.PLC
	GraphQL       *graphql.Server
	ModbusClients map[string]*modbus.Client
	OPCUAClients  map[string]*opcua.Client
	MQTTClients   map[string]*mqtt.Client
	RedisClients  map[string]*redis.Client
	RESTClients   map[string]*rest.Client

	config Config
	logger *slog.Logger
}

// Config is the PLCGo configuration.
type Config = plc.Config

// Option is a functional option for configuring PLCGo.
type Option func(*PLCGo)

// WithLogger sets a custom logger.
func WithLogger(logger *slog.Logger) Option {
	return func(p *PLCGo) {
		p.logger = logger
	}
}

// New creates a new PLCGo instance.
func New(config Config, opts ...Option) (*PLCGo, error) {
	pg := &PLCGo{
		config:        config,
		ModbusClients: make(map[string]*modbus.Client),
		OPCUAClients:  make(map[string]*opcua.Client),
		MQTTClients:   make(map[string]*mqtt.Client),
		RedisClients:  make(map[string]*redis.Client),
		RESTClients:   make(map[string]*rest.Client),
		logger:        slog.Default(),
	}

	// Apply options
	for _, opt := range opts {
		opt(pg)
	}

	// Create PLC runtime
	pg.PLC = plc.New(config, plc.WithLogger(pg.logger))

	// Create GraphQL server
	pg.GraphQL = graphql.NewServer(pg.PLC)

	// Initialize Modbus clients
	for id, cfg := range config.Sources.Modbus {
		cfg.ID = id
		cfg.Type = plc.SourceTypeModbus
		client := modbus.NewClient(cfg)
		pg.ModbusClients[id] = client
		pg.PLC.RegisterModbusClient(id, client)
	}

	// Initialize OPC UA clients
	for id, cfg := range config.Sources.OPCUA {
		cfg.ID = id
		cfg.Type = plc.SourceTypeOPCUA
		client := opcua.NewClient(cfg)
		pg.OPCUAClients[id] = client
		pg.PLC.RegisterOPCUAClient(id, client)
	}

	// Initialize MQTT clients
	for id, cfg := range config.Sources.MQTT {
		cfg.ID = id
		cfg.Type = plc.SourceTypeMQTT
		client := mqtt.NewClient(cfg)
		pg.MQTTClients[id] = client
		pg.PLC.RegisterMQTTClient(id, client)
	}

	// Also initialize MQTT clients from the main MQTT config
	for id, cfg := range config.MQTT {
		if _, exists := pg.MQTTClients[id]; !exists {
			cfg.ID = id
			cfg.Type = plc.SourceTypeMQTT
			client := mqtt.NewClient(cfg)
			pg.MQTTClients[id] = client
			pg.PLC.RegisterMQTTClient(id, client)
		}
	}

	// Initialize Redis clients
	for id, cfg := range config.Sources.Redis {
		cfg.ID = id
		cfg.Type = plc.SourceTypeRedis
		client := redis.NewClient(cfg)
		pg.RedisClients[id] = client
		pg.PLC.RegisterRedisClient(id, client)
	}

	// Initialize REST clients
	for id, cfg := range config.Sources.REST {
		cfg.ID = id
		cfg.Type = plc.SourceTypeREST
		client := rest.NewClient(cfg)
		pg.RESTClients[id] = client
		pg.PLC.RegisterRESTClient(id, client)
	}

	return pg, nil
}

// RegisterTask registers a task program.
func (pg *PLCGo) RegisterTask(taskID string, program plc.TaskProgram) error {
	return pg.PLC.RegisterTask(taskID, program)
}

// Start starts the PLCGo runtime and connects all sources.
func (pg *PLCGo) Start(ctx context.Context) error {
	pg.logger.Info("Starting PLCGo", "id", pg.config.ID, "name", pg.config.Name)

	// Connect all enabled sources
	for id, client := range pg.ModbusClients {
		cfg := client.Config()
		if cfg.Enabled {
			pg.logger.Info("Connecting Modbus source", "id", id, "host", cfg.Host, "port", cfg.Port)
			if err := client.Connect(ctx); err != nil {
				pg.logger.Warn("Failed to connect Modbus source", "id", id, "error", err)
			}
		}
	}

	for id, client := range pg.OPCUAClients {
		cfg := client.Config()
		if cfg.Enabled {
			pg.logger.Info("Connecting OPC UA source", "id", id, "endpoint", cfg.Endpoint)
			if err := client.Connect(ctx); err != nil {
				pg.logger.Warn("Failed to connect OPC UA source", "id", id, "error", err)
			}
		}
	}

	for id, client := range pg.MQTTClients {
		cfg := client.Config()
		if cfg.Enabled {
			pg.logger.Info("Connecting MQTT source", "id", id, "broker", cfg.ServerURL)
			if err := client.Connect(ctx); err != nil {
				pg.logger.Warn("Failed to connect MQTT source", "id", id, "error", err)
			}
		}
	}

	for id, client := range pg.RedisClients {
		cfg := client.Config()
		if cfg.Enabled {
			pg.logger.Info("Connecting Redis source", "id", id, "url", cfg.URL)
			if err := client.Connect(ctx); err != nil {
				pg.logger.Warn("Failed to connect Redis source", "id", id, "error", err)
			}
		}
	}

	// Start PLC runtime
	if err := pg.PLC.Start(ctx); err != nil {
		return fmt.Errorf("failed to start PLC runtime: %w", err)
	}

	pg.logger.Info("PLCGo started successfully")
	return nil
}

// StartGraphQL starts the GraphQL server.
func (pg *PLCGo) StartGraphQL(addr string) error {
	pg.logger.Info("Starting GraphQL server", "addr", addr)
	return pg.GraphQL.Start(addr)
}

// Stop stops the PLCGo runtime and disconnects all sources.
func (pg *PLCGo) Stop(ctx context.Context) error {
	pg.logger.Info("Stopping PLCGo")

	// Stop GraphQL server
	if err := pg.GraphQL.Stop(ctx); err != nil {
		pg.logger.Warn("Error stopping GraphQL server", "error", err)
	}

	// Stop PLC runtime
	if err := pg.PLC.Stop(); err != nil {
		pg.logger.Warn("Error stopping PLC runtime", "error", err)
	}

	// Disconnect all sources
	for id, client := range pg.ModbusClients {
		if err := client.Disconnect(); err != nil {
			pg.logger.Warn("Error disconnecting Modbus source", "id", id, "error", err)
		}
	}

	for id, client := range pg.OPCUAClients {
		if err := client.Disconnect(ctx); err != nil {
			pg.logger.Warn("Error disconnecting OPC UA source", "id", id, "error", err)
		}
	}

	for id, client := range pg.MQTTClients {
		if err := client.Disconnect(); err != nil {
			pg.logger.Warn("Error disconnecting MQTT source", "id", id, "error", err)
		}
	}

	for id, client := range pg.RedisClients {
		if err := client.Disconnect(); err != nil {
			pg.logger.Warn("Error disconnecting Redis source", "id", id, "error", err)
		}
	}

	pg.logger.Info("PLCGo stopped")
	return nil
}

// SetValue sets a variable's value.
func (pg *PLCGo) SetValue(variableID string, value interface{}) error {
	return pg.PLC.SetValue(variableID, value)
}

// GetValue gets a variable's current value.
func (pg *PLCGo) GetValue(variableID string) (interface{}, error) {
	return pg.PLC.GetValue(variableID)
}

// Re-export types for convenience
type (
	// DataType represents the type of a variable's value.
	DataType = plc.DataType
	// SourceType represents the type of data source.
	SourceType = plc.SourceType
	// ConnectionState represents the current state of a source connection.
	ConnectionState = plc.ConnectionState
	// ModbusRegisterType represents the type of Modbus register.
	ModbusRegisterType = plc.ModbusRegisterType
	// ModbusFormat represents the data format for Modbus registers.
	ModbusFormat = plc.ModbusFormat
	// Duration is a wrapper around time.Duration for JSON marshaling.
	Duration = plc.Duration
	// TaskProgram is a function that executes task logic.
	TaskProgram = plc.TaskProgram
	// TaskContext provides access to variables and update function within a task.
	TaskContext = plc.TaskContext
	// Variable represents a runtime variable with its config and current value.
	Variable = plc.Variable
	// VariableConfig defines a PLC variable.
	VariableConfig = plc.VariableConfig
	// VariableSource defines how a variable reads/writes data from a source.
	VariableSource = plc.VariableSource
	// TaskConfig defines a scheduled task.
	TaskConfig = plc.TaskConfig
	// Sources holds all configured data sources.
	Sources = plc.Sources
	// ModbusSourceConfig extends SourceConfig for Modbus sources.
	ModbusSourceConfig = plc.ModbusSourceConfig
	// OPCUASourceConfig extends SourceConfig for OPC UA sources.
	OPCUASourceConfig = plc.OPCUASourceConfig
	// MQTTSourceConfig extends SourceConfig for MQTT sources.
	MQTTSourceConfig = plc.MQTTSourceConfig
	// RedisSourceConfig extends SourceConfig for Redis sources.
	RedisSourceConfig = plc.RedisSourceConfig
	// RESTSourceConfig extends SourceConfig for REST sources.
	RESTSourceConfig = plc.RESTSourceConfig
)

// Constants
const (
	DataTypeNumber  = plc.DataTypeNumber
	DataTypeBoolean = plc.DataTypeBoolean
	DataTypeString  = plc.DataTypeString
	DataTypeUDT     = plc.DataTypeUDT

	SourceTypeModbus = plc.SourceTypeModbus
	SourceTypeOPCUA  = plc.SourceTypeOPCUA
	SourceTypeMQTT   = plc.SourceTypeMQTT
	SourceTypeRedis  = plc.SourceTypeRedis
	SourceTypeREST   = plc.SourceTypeREST

	StateDisconnected = plc.StateDisconnected
	StateConnecting   = plc.StateConnecting
	StateConnected    = plc.StateConnected
	StateError        = plc.StateError

	ModbusHoldingRegister = plc.ModbusHoldingRegister
	ModbusInputRegister   = plc.ModbusInputRegister
	ModbusCoil            = plc.ModbusCoil
	ModbusDiscreteInput   = plc.ModbusDiscreteInput

	ModbusFormatInt16   = plc.ModbusFormatInt16
	ModbusFormatUint16  = plc.ModbusFormatUint16
	ModbusFormatInt32   = plc.ModbusFormatInt32
	ModbusFormatUint32  = plc.ModbusFormatUint32
	ModbusFormatFloat32 = plc.ModbusFormatFloat32
	ModbusFormatFloat64 = plc.ModbusFormatFloat64
	ModbusFormatString  = plc.ModbusFormatString
	ModbusFormatBoolean = plc.ModbusFormatBoolean
)
