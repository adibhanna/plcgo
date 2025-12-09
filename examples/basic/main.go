// Example basic demonstrates a simple PLCGo application with multiple data sources.
//
// This example shows:
// - Creating a PLC with Modbus and MQTT sources
// - Defining variables that read from Modbus registers
// - Running a periodic task that processes data
// - Exposing the PLC state via GraphQL API
package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/adibhanna/plcgo"
)

func main() {
	// Configure logging
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Define PLC configuration
	config := plcgo.Config{
		ID:          "plc-001",
		Name:        "Example PLC",
		Description: "A basic PLCGo example with Modbus and MQTT",

		// Define data sources
		Sources: plcgo.Sources{
			// Modbus TCP source
			Modbus: map[string]plcgo.ModbusSourceConfig{
				"modbus1": {
					SourceConfig: plcgo.SourceConfig{
						ID:            "modbus1",
						Type:          plcgo.SourceTypeModbus,
						Name:          "Main Modbus Server",
						Description:   "Primary Modbus TCP connection",
						Enabled:       true,
						RetryMinDelay: plcgo.Duration(time.Second),
						RetryMaxDelay: plcgo.Duration(30 * time.Second),
					},
					Host:         "localhost",
					Port:         502,
					UnitID:       1,
					Timeout:      plcgo.Duration(3 * time.Second),
					ReverseBits:  false,
					ReverseWords: false,
				},
			},
			// MQTT source
			MQTT: map[string]plcgo.MQTTSourceConfig{
				"mqtt1": {
					SourceConfig: plcgo.SourceConfig{
						ID:            "mqtt1",
						Type:          plcgo.SourceTypeMQTT,
						Name:          "Local MQTT Broker",
						Description:   "MQTT connection with Sparkplug B",
						Enabled:       true,
						RetryMinDelay: plcgo.Duration(time.Second),
						RetryMaxDelay: plcgo.Duration(30 * time.Second),
					},
					ServerURL:    "tcp://localhost:1883",
					ClientID:     "plcgo-example",
					CleanSession: true,
					// Sparkplug B settings
					GroupID:    "MyGroup",
					EdgeNodeID: "MyEdgeNode",
					DeviceID:   "MyDevice",
				},
			},
		},

		// Define variables
		Variables: map[string]plcgo.VariableConfig{
			"counter": {
				ID:          "counter",
				Name:        "Counter",
				Description: "A simple counter that increments every scan",
				DataType:    plcgo.DataTypeNumber,
				Default:     float64(0),
				Decimals:    0,
			},
			"temperature": {
				ID:          "temperature",
				Name:        "Temperature",
				Description: "Temperature reading from Modbus",
				DataType:    plcgo.DataTypeNumber,
				Default:     float64(0),
				Decimals:    2,
				Deadband:    0.5, // Only publish if change > 0.5
				Source: &plcgo.VariableSource{
					SourceID:     "modbus1",
					Type:         plcgo.SourceTypeModbus,
					Register:     0,
					RegisterType: plcgo.ModbusInputRegister,
					Format:       plcgo.ModbusFormatFloat32,
					Rate:         plcgo.Duration(time.Second),
				},
			},
			"setpoint": {
				ID:          "setpoint",
				Name:        "Setpoint",
				Description: "Temperature setpoint (writable)",
				DataType:    plcgo.DataTypeNumber,
				Default:     float64(25.0),
				Decimals:    1,
				Writable:    true,
				Source: &plcgo.VariableSource{
					SourceID:      "modbus1",
					Type:          plcgo.SourceTypeModbus,
					Register:      10,
					RegisterType:  plcgo.ModbusHoldingRegister,
					Format:        plcgo.ModbusFormatFloat32,
					Rate:          plcgo.Duration(time.Second),
					Bidirectional: true,
				},
			},
			"running": {
				ID:          "running",
				Name:        "Running",
				Description: "System running status",
				DataType:    plcgo.DataTypeBoolean,
				Default:     false,
				Source: &plcgo.VariableSource{
					SourceID:     "modbus1",
					Type:         plcgo.SourceTypeModbus,
					Register:     100,
					RegisterType: plcgo.ModbusCoil,
					Format:       plcgo.ModbusFormatBoolean,
					Rate:         plcgo.Duration(500 * time.Millisecond),
				},
			},
			"status": {
				ID:          "status",
				Name:        "Status",
				Description: "System status message",
				DataType:    plcgo.DataTypeString,
				Default:     "Initializing",
			},
		},

		// Define tasks
		Tasks: map[string]plcgo.TaskConfig{
			"main": {
				ID:          "main",
				Name:        "Main Task",
				Description: "Main control task running at 1 second interval",
				ScanRate:    plcgo.Duration(time.Second),
				Enabled:     true,
			},
			"fast": {
				ID:          "fast",
				Name:        "Fast Task",
				Description: "Fast data acquisition task",
				ScanRate:    plcgo.Duration(100 * time.Millisecond),
				Enabled:     true,
			},
		},
	}

	// Create PLCGo instance
	pg, err := plcgo.New(config, plcgo.WithLogger(logger))
	if err != nil {
		log.Fatalf("Failed to create PLCGo: %v", err)
	}

	// Register task programs
	err = pg.RegisterTask("main", func(ctx plcgo.TaskContext) error {
		// Get current counter value
		counterVar, ok := ctx.Variables["counter"]
		if !ok {
			return nil
		}

		counter, _ := counterVar.GetValue().Value.(float64)

		// Increment counter
		counter++
		ctx.SetValue("counter", counter)

		// Update status based on counter
		var status string
		if int(counter)%10 == 0 {
			status = "Processing batch"
		} else {
			status = "Running normally"
		}
		ctx.SetValue("status", status)

		return nil
	})
	if err != nil {
		log.Fatalf("Failed to register main task: %v", err)
	}

	err = pg.RegisterTask("fast", func(ctx plcgo.TaskContext) error {
		// Fast task logic - typically used for data acquisition
		// In a real application, this would read from sources
		return nil
	})
	if err != nil {
		log.Fatalf("Failed to register fast task: %v", err)
	}

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start PLCGo
	if err := pg.Start(ctx); err != nil {
		log.Fatalf("Failed to start PLCGo: %v", err)
	}

	// Start GraphQL server in a goroutine
	go func() {
		if err := pg.StartGraphQL(":4000"); err != nil {
			logger.Error("GraphQL server error", "error", err)
		}
	}()

	logger.Info("PLCGo is running",
		"graphql", "http://localhost:4000/graphql",
		"playground", "http://localhost:4000/",
	)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	logger.Info("Shutting down...")

	// Stop PLCGo
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := pg.Stop(shutdownCtx); err != nil {
		logger.Error("Error during shutdown", "error", err)
	}

	logger.Info("Goodbye!")
}
