// Command plcgo runs a PLCGo instance from a configuration file.
//
// Usage:
//
//	plcgo --config config.json
//
// Environment variables:
//
//	PLCGO_CONFIG    - Path to configuration file (default: config.json)
//	PLCGO_PORT      - GraphQL server port (default: 4000)
//	PLCGO_HOST      - GraphQL server host (default: 0.0.0.0)
//	LOG_LEVEL       - Log level: debug, info, warn, error (default: info)
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/adibhanna/plcgo"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	// Parse flags
	configPath := flag.String("config", getEnv("PLCGO_CONFIG", "config.json"), "Path to configuration file")
	port := flag.String("port", getEnv("PLCGO_PORT", "4000"), "GraphQL server port")
	host := flag.String("host", getEnv("PLCGO_HOST", "0.0.0.0"), "GraphQL server host")
	logLevel := flag.String("log-level", getEnv("LOG_LEVEL", "info"), "Log level (debug, info, warn, error)")
	showVersion := flag.Bool("version", false, "Show version information")
	flag.Parse()

	if *showVersion {
		fmt.Printf("plcgo %s (commit: %s, built: %s)\n", version, commit, date)
		os.Exit(0)
	}

	// Configure logger
	var level slog.Level
	switch *logLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))

	logger.Info("Starting PLCGo",
		"version", version,
		"commit", commit,
		"built", date,
	)

	// Load configuration
	config, err := loadConfig(*configPath)
	if err != nil {
		logger.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Create PLCGo instance
	pg, err := plcgo.New(*config, plcgo.WithLogger(logger))
	if err != nil {
		logger.Error("Failed to create PLCGo instance", "error", err)
		os.Exit(1)
	}

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start PLCGo
	if err := pg.Start(ctx); err != nil {
		logger.Error("Failed to start PLCGo", "error", err)
		os.Exit(1)
	}

	// Start GraphQL server
	addr := fmt.Sprintf("%s:%s", *host, *port)
	go func() {
		logger.Info("Starting GraphQL server", "addr", addr)
		if err := pg.StartGraphQL(addr); err != nil {
			logger.Error("GraphQL server error", "error", err)
		}
	}()

	logger.Info("PLCGo is running",
		"config", *configPath,
		"graphql", fmt.Sprintf("http://%s/graphql", addr),
		"playground", fmt.Sprintf("http://%s/", addr),
	)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	logger.Info("Received shutdown signal", "signal", sig)

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := pg.Stop(shutdownCtx); err != nil {
		logger.Error("Error during shutdown", "error", err)
		os.Exit(1)
	}

	logger.Info("PLCGo stopped gracefully")
}

func loadConfig(path string) (*plcgo.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config plcgo.Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &config, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
