# PLCGo

A modern, production-ready software PLC (Programmable Logic Controller) implementation in Go. PLCGo provides multi-protocol connectivity for industrial automation with a GraphQL API for easy integration.

[![Go Version](https://img.shields.io/badge/Go-1.23+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

## Features

- **Multi-Protocol Support**
  - **Modbus TCP/IP** - Full implementation using [modbus-go](https://github.com/adibhanna/modbus-go)
  - **OPC UA** - Client support for OPC Unified Architecture
  - **MQTT** - With Sparkplug B specification support
  - **Redis** - For state persistence and pub/sub
  - **REST** - HTTP/HTTPS API integration

- **GraphQL API** - Modern API for monitoring and control
- **Task Scheduling** - Configurable scan rates for periodic execution
- **Variable Management** - Type-safe variables with deadband, quality, and timestamps
- **Automatic Reconnection** - Exponential backoff retry for all protocols
- **Docker Ready** - Containerized deployment support

## Quick Start

### Installation

```bash
go get github.com/adibhanna/plcgo
```

### Basic Example

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/adibhanna/plcgo"
)

func main() {
    // Define configuration
    config := plcgo.Config{
        ID:   "plc-001",
        Name: "My PLC",
        Variables: map[string]plcgo.VariableConfig{
            "counter": {
                ID:       "counter",
                Name:     "Counter",
                DataType: plcgo.DataTypeNumber,
                Default:  float64(0),
            },
        },
        Tasks: map[string]plcgo.TaskConfig{
            "main": {
                ID:       "main",
                Name:     "Main Task",
                ScanRate: plcgo.Duration(time.Second),
                Enabled:  true,
            },
        },
    }

    // Create PLCGo instance
    pg, err := plcgo.New(config)
    if err != nil {
        log.Fatal(err)
    }

    // Register task program
    pg.RegisterTask("main", func(ctx plcgo.TaskContext) error {
        counter, _ := ctx.Variables["counter"].GetValue().Value.(float64)
        return ctx.SetValue("counter", counter+1)
    })

    // Start PLC
    ctx := context.Background()
    pg.Start(ctx)

    // Start GraphQL server
    pg.StartGraphQL(":4000")
}
```

## Architecture

PLCGo follows a modular architecture inspired by [](https://github.com/joyautomation/):

```
┌─────────────────────────────────────────────────────────────┐
│                         PLCGo                                │
├─────────────────────────────────────────────────────────────┤
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐          │
│  │   Modbus    │  │   OPC UA    │  │    MQTT     │          │
│  │   Client    │  │   Client    │  │   Client    │          │
│  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘          │
│         │                │                │                  │
│  ┌──────┴────────────────┴────────────────┴──────┐          │
│  │              Source Manager                     │          │
│  └──────────────────────┬─────────────────────────┘          │
│                         │                                    │
│  ┌──────────────────────┴─────────────────────────┐          │
│  │              Variable Store                     │          │
│  └──────────────────────┬─────────────────────────┘          │
│                         │                                    │
│  ┌──────────────────────┴─────────────────────────┐          │
│  │              Task Scheduler                     │          │
│  └──────────────────────┬─────────────────────────┘          │
│                         │                                    │
│  ┌──────────────────────┴─────────────────────────┐          │
│  │              GraphQL API                        │          │
│  └─────────────────────────────────────────────────┘          │
└─────────────────────────────────────────────────────────────┘
```

## Configuration

PLCGo uses JSON configuration files. See [config/config.example.json](config/config.example.json) for a complete example.

### Sources

#### Modbus TCP

```json
{
  "sources": {
    "modbus": {
      "modbus1": {
        "id": "modbus1",
        "type": "modbus",
        "name": "Main Modbus Server",
        "enabled": true,
        "host": "192.168.1.100",
        "port": 502,
        "unitId": 1,
        "timeout": "3s",
        "reverseBits": false,
        "reverseWords": false
      }
    }
  }
}
```

#### OPC UA

```json
{
  "sources": {
    "opcua": {
      "opcua1": {
        "id": "opcua1",
        "type": "opcua",
        "name": "OPC UA Server",
        "enabled": true,
        "endpoint": "opc.tcp://localhost:4840",
        "securityPolicy": "None",
        "securityMode": "None"
      }
    }
  }
}
```

#### MQTT with Sparkplug B

```json
{
  "sources": {
    "mqtt": {
      "mqtt1": {
        "id": "mqtt1",
        "type": "mqtt",
        "name": "MQTT Broker",
        "enabled": true,
        "serverUrl": "tcp://localhost:1883",
        "clientId": "plcgo-001",
        "groupId": "MyGroup",
        "edgeNodeId": "MyEdgeNode",
        "deviceId": "MyDevice"
      }
    }
  }
}
```

### Variables

Variables define data points that can be read from sources or set programmatically:

```json
{
  "variables": {
    "temperature": {
      "id": "temperature",
      "name": "Temperature",
      "description": "Process temperature",
      "datatype": "number",
      "default": 0,
      "decimals": 2,
      "deadband": 0.5,
      "source": {
        "sourceId": "modbus1",
        "type": "modbus",
        "register": 0,
        "registerType": "INPUT_REGISTER",
        "format": "FLOAT32",
        "rate": "1s"
      }
    }
  }
}
```

#### Supported Data Types

| Type | Description |
|------|-------------|
| `number` | Numeric values (int, float) |
| `boolean` | True/false values |
| `string` | Text values |
| `udt` | User-defined types |

#### Modbus Register Types

| Type | Description |
|------|-------------|
| `HOLDING_REGISTER` | Read/write registers (FC 03, 06, 16) |
| `INPUT_REGISTER` | Read-only registers (FC 04) |
| `COIL` | Read/write bits (FC 01, 05, 15) |
| `DISCRETE_INPUT` | Read-only bits (FC 02) |

#### Modbus Data Formats

| Format | Registers | Description |
|--------|-----------|-------------|
| `INT16` | 1 | 16-bit signed integer |
| `UINT16` | 1 | 16-bit unsigned integer |
| `INT32` | 2 | 32-bit signed integer |
| `UINT32` | 2 | 32-bit unsigned integer |
| `FLOAT32` | 2 | 32-bit float (IEEE 754) |
| `FLOAT64` | 4 | 64-bit float (IEEE 754) |
| `BOOLEAN` | 1 | Boolean (0 = false) |
| `STRING` | N | ASCII string |

### Tasks

Tasks are periodic programs that execute at specified intervals:

```json
{
  "tasks": {
    "main": {
      "id": "main",
      "name": "Main Control Task",
      "description": "Primary control logic",
      "scanRate": "1s",
      "enabled": true
    },
    "fast": {
      "id": "fast",
      "name": "Fast Acquisition",
      "scanRate": "100ms",
      "enabled": true
    }
  }
}
```

## GraphQL API

PLCGo exposes a GraphQL API for monitoring and control. Access the GraphQL Playground at `http://localhost:4000/`.

### Query Examples

```graphql
# Get all PLC data
query {
  plc {
    config {
      id
      name
      description
    }
    variables {
      id
      name
      value
      quality
      timestamp
    }
    tasks {
      id
      name
      running
      executionCount
      metrics {
        executeDuration
      }
    }
  }
}
```

### Mutation Examples

```graphql
# Set a variable value
mutation {
  setValue(variableId: "setpoint", value: "25.5") {
    variables {
      id
      value
    }
  }
}
```

## Docker Deployment

### Using Docker Compose

```bash
# Start all services
docker-compose up -d

# View logs
docker-compose logs -f plcgo

# Stop all services
docker-compose down
```

### Building the Image

```bash
# Build
docker build -t plcgo .

# Run
docker run -p 4000:4000 -v $(pwd)/config:/app/config plcgo
```

### Docker Compose Services

The included `docker-compose.yml` provides:

| Service | Port | Description |
|---------|------|-------------|
| `plcgo` | 4000 | PLCGo GraphQL API |
| `redis` | 6379 | State persistence |
| `mosquitto` | 1883, 9001 | MQTT broker |

## Development

### Prerequisites

- Go 1.23+
- Docker (optional, for containerized deployment)

### Building

```bash
# Build
go build -o plcgo ./cmd/plcgo

# Run tests
go test ./...

# Run with race detector
go test -race ./...
```

### Project Structure

```
plcgo/
├── cmd/
│   └── plcgo/          # CLI application
├── pkg/
│   ├── plc/            # Core PLC types and runtime
│   ├── modbus/         # Modbus client
│   ├── opcua/          # OPC UA client
│   ├── mqtt/           # MQTT client with Sparkplug
│   ├── redis/          # Redis client
│   ├── rest/           # REST client
│   └── graphql/        # GraphQL server
├── examples/
│   └── basic/          # Basic usage example
├── config/             # Configuration examples
├── docker/             # Docker configuration files
├── Dockerfile
├── docker-compose.yml
└── plcgo.go            # Main package entry point
```

## API Reference

### PLCGo

```go
// Create a new PLCGo instance
pg, err := plcgo.New(config, plcgo.WithLogger(logger))

// Register a task program
pg.RegisterTask("taskId", func(ctx plcgo.TaskContext) error {
    // Task logic
    return nil
})

// Start the PLC runtime
pg.Start(ctx)

// Start the GraphQL server
pg.StartGraphQL(":4000")

// Stop everything
pg.Stop(ctx)

// Get/Set variable values
value, err := pg.GetValue("variableId")
err = pg.SetValue("variableId", newValue)
```

### TaskContext

```go
type TaskContext struct {
    Variables map[string]*Variable  // All variables
    SetValue  func(id string, value interface{}) error
}

// Example task
func myTask(ctx plcgo.TaskContext) error {
    // Read a variable
    temp := ctx.Variables["temperature"].GetValue().Value.(float64)

    // Write a variable
    ctx.SetValue("output", temp * 1.5)

    return nil
}
```

## Comparison with 

PLCGo is inspired by [](https://github.com/joyautomation/) and aims to provide similar functionality in Go:

| Feature | PLCGo |  |
|---------|-------|----------|
| Language | Go | TypeScript/Deno |
| Modbus TCP | ✅ | ✅ |
| Modbus RTU | ✅ (via modbus-go) | ❌ |
| OPC UA | ✅ | ✅ |
| MQTT/Sparkplug | ✅ | ✅ |
| Redis | ✅ | ✅ |
| REST | ✅ | ✅ |
| GraphQL API | ✅ | ✅ |
| Task Scheduling | ✅ | ✅ |
| Docker Support | ✅ | ✅ |

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

This project is licensed under the Apache 2.0 License - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- [](https://github.com/joyautomation/) - Inspiration for architecture and API design
- [modbus-go](https://github.com/adibhanna/modbus-go) - Modbus protocol implementation
- [gopcua](https://github.com/gopcua/opcua) - OPC UA client library
- [paho.mqtt.golang](https://github.com/eclipse/paho.mqtt.golang) - MQTT client library
- [go-redis](https://github.com/redis/go-redis) - Redis client library
