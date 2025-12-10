// Package graphql provides a GraphQL API for the PLC runtime.
package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/adibhanna/plcgo/pkg/plc"
)

// Server provides a GraphQL HTTP server for the PLC.
type Server struct {
	plc       *plc.PLC
	mux       *http.ServeMux
	pubsub    *PubSub
	server    *http.Server
}

// PubSub manages GraphQL subscriptions.
type PubSub struct {
	subscribers map[string]chan []byte
	mu          sync.RWMutex
}

// NewPubSub creates a new PubSub instance.
func NewPubSub() *PubSub {
	return &PubSub{
		subscribers: make(map[string]chan []byte),
	}
}

// Subscribe creates a new subscription.
func (ps *PubSub) Subscribe(id string) chan []byte {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ch := make(chan []byte, 10)
	ps.subscribers[id] = ch
	return ch
}

// Unsubscribe removes a subscription.
func (ps *PubSub) Unsubscribe(id string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ch, exists := ps.subscribers[id]; exists {
		close(ch)
		delete(ps.subscribers, id)
	}
}

// Publish sends data to all subscribers.
func (ps *PubSub) Publish(data []byte) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	for _, ch := range ps.subscribers {
		select {
		case ch <- data:
		default:
			// Channel full, skip
		}
	}
}

// NewServer creates a new GraphQL server.
func NewServer(p *plc.PLC) *Server {
	s := &Server{
		plc:    p,
		mux:    http.NewServeMux(),
		pubsub: NewPubSub(),
	}

	s.mux.HandleFunc("/graphql", s.handleGraphQL)
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/", s.handlePlayground)

	return s
}

// Start starts the HTTP server.
func (s *Server) Start(addr string) error {
	s.server = &http.Server{
		Addr:    addr,
		Handler: s.mux,
	}

	// Start publishing PLC updates
	go s.publishUpdates()

	return s.server.ListenAndServe()
}

// Stop stops the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

func (s *Server) publishUpdates() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		data, err := s.plc.ToJSON()
		if err != nil {
			continue
		}
		s.pubsub.Publish(data)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handlePlayground(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	_, _ = w.Write([]byte(graphqlPlayground))
}

func (s *Server) handleGraphQL(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	var request struct {
		Query         string                 `json:"query"`
		OperationName string                 `json:"operationName"`
		Variables     map[string]interface{} `json:"variables"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	result := s.executeQuery(r.Context(), request.Query, request.Variables)
	_ = json.NewEncoder(w).Encode(result)
}

func (s *Server) executeQuery(ctx context.Context, query string, variables map[string]interface{}) map[string]interface{} {
	// Simple query parser - handles basic queries
	result := make(map[string]interface{})
	data := make(map[string]interface{})

	// Handle introspection
	if containsString(query, "__schema") || containsString(query, "__type") {
		return s.handleIntrospection(query)
	}

	// Handle queries
	if containsString(query, "plc") || containsString(query, "query") {
		plcData := s.buildPLCResponse()

		if containsString(query, "config") {
			data["config"] = plcData["config"]
		}
		if containsString(query, "variables") {
			data["variables"] = plcData["variables"]
		}
		if containsString(query, "tasks") {
			data["tasks"] = plcData["tasks"]
		}

		// If no specific fields requested, return all
		if len(data) == 0 {
			data = plcData
		}

		result["data"] = map[string]interface{}{"plc": data}
	}

	// Handle mutations
	if containsString(query, "mutation") {
		if containsString(query, "setValue") {
			variableID, _ := variables["variableId"].(string)
			value := variables["value"]

			if variableID != "" {
				if err := s.plc.SetValue(variableID, value); err != nil {
					result["errors"] = []map[string]string{{"message": err.Error()}}
				} else {
					result["data"] = map[string]interface{}{
						"setValue": s.buildPLCResponse(),
					}
				}
			}
		}
	}

	return result
}

func (s *Server) buildPLCResponse() map[string]interface{} {
	config := s.plc.GetConfig()
	variables := s.plc.GetVariables()
	tasks := s.plc.GetTasks()

	varsData := make([]map[string]interface{}, 0, len(variables))
	for id, v := range variables {
		val := v.GetValue()
		varsData = append(varsData, map[string]interface{}{
			"id":          id,
			"name":        v.Config.Name,
			"description": v.Config.Description,
			"datatype":    string(v.Config.DataType),
			"value":       fmt.Sprintf("%v", val.Value),
			"quality":     val.Quality,
			"timestamp":   val.Timestamp.Format(time.RFC3339),
			"error":       formatError(val.Error),
		})
	}

	tasksData := make([]map[string]interface{}, 0, len(tasks))
	for id, t := range tasks {
		snap := t.Snapshot()
		tasksData = append(tasksData, map[string]interface{}{
			"id":             id,
			"name":           snap.Config.Name,
			"description":    snap.Config.Description,
			"scanRate":       int64(snap.Config.ScanRate.Duration().Milliseconds()),
			"enabled":        snap.Config.Enabled,
			"running":        snap.Running,
			"executionCount": snap.ExecutionCount,
			"errorCount":     snap.ErrorCount,
			"lastError":      formatError(snap.LastError),
			"metrics": map[string]interface{}{
				"waitDuration":    snap.Metrics.WaitDuration.Milliseconds(),
				"executeDuration": snap.Metrics.ExecuteDuration.Milliseconds(),
			},
		})
	}

	return map[string]interface{}{
		"config": map[string]interface{}{
			"id":          config.ID,
			"name":        config.Name,
			"description": config.Description,
		},
		"variables": varsData,
		"tasks":     tasksData,
	}
}

func (s *Server) handleIntrospection(query string) map[string]interface{} {
	schema := map[string]interface{}{
		"__schema": map[string]interface{}{
			"queryType":        map[string]string{"name": "Query"},
			"mutationType":     map[string]string{"name": "Mutation"},
			"subscriptionType": map[string]string{"name": "Subscription"},
			"types": []map[string]interface{}{
				{
					"kind": "OBJECT",
					"name": "Query",
					"fields": []map[string]interface{}{
						{
							"name": "plc",
							"type": map[string]interface{}{
								"kind": "OBJECT",
								"name": "PLC",
							},
						},
					},
				},
				{
					"kind": "OBJECT",
					"name": "PLC",
					"fields": []map[string]interface{}{
						{"name": "config", "type": map[string]interface{}{"kind": "OBJECT", "name": "PLCConfig"}},
						{"name": "variables", "type": map[string]interface{}{"kind": "LIST", "ofType": map[string]interface{}{"kind": "OBJECT", "name": "Variable"}}},
						{"name": "tasks", "type": map[string]interface{}{"kind": "LIST", "ofType": map[string]interface{}{"kind": "OBJECT", "name": "Task"}}},
					},
				},
				{
					"kind": "OBJECT",
					"name": "PLCConfig",
					"fields": []map[string]interface{}{
						{"name": "id", "type": map[string]interface{}{"kind": "SCALAR", "name": "String"}},
						{"name": "name", "type": map[string]interface{}{"kind": "SCALAR", "name": "String"}},
						{"name": "description", "type": map[string]interface{}{"kind": "SCALAR", "name": "String"}},
					},
				},
				{
					"kind": "OBJECT",
					"name": "Variable",
					"fields": []map[string]interface{}{
						{"name": "id", "type": map[string]interface{}{"kind": "SCALAR", "name": "String"}},
						{"name": "name", "type": map[string]interface{}{"kind": "SCALAR", "name": "String"}},
						{"name": "description", "type": map[string]interface{}{"kind": "SCALAR", "name": "String"}},
						{"name": "datatype", "type": map[string]interface{}{"kind": "SCALAR", "name": "String"}},
						{"name": "value", "type": map[string]interface{}{"kind": "SCALAR", "name": "String"}},
						{"name": "quality", "type": map[string]interface{}{"kind": "SCALAR", "name": "String"}},
						{"name": "timestamp", "type": map[string]interface{}{"kind": "SCALAR", "name": "String"}},
						{"name": "error", "type": map[string]interface{}{"kind": "OBJECT", "name": "Error"}},
					},
				},
				{
					"kind": "OBJECT",
					"name": "Task",
					"fields": []map[string]interface{}{
						{"name": "id", "type": map[string]interface{}{"kind": "SCALAR", "name": "String"}},
						{"name": "name", "type": map[string]interface{}{"kind": "SCALAR", "name": "String"}},
						{"name": "description", "type": map[string]interface{}{"kind": "SCALAR", "name": "String"}},
						{"name": "scanRate", "type": map[string]interface{}{"kind": "SCALAR", "name": "Int"}},
						{"name": "enabled", "type": map[string]interface{}{"kind": "SCALAR", "name": "Boolean"}},
						{"name": "running", "type": map[string]interface{}{"kind": "SCALAR", "name": "Boolean"}},
						{"name": "executionCount", "type": map[string]interface{}{"kind": "SCALAR", "name": "Int"}},
						{"name": "errorCount", "type": map[string]interface{}{"kind": "SCALAR", "name": "Int"}},
						{"name": "metrics", "type": map[string]interface{}{"kind": "OBJECT", "name": "TaskMetrics"}},
					},
				},
				{
					"kind": "OBJECT",
					"name": "TaskMetrics",
					"fields": []map[string]interface{}{
						{"name": "waitDuration", "type": map[string]interface{}{"kind": "SCALAR", "name": "Int"}},
						{"name": "executeDuration", "type": map[string]interface{}{"kind": "SCALAR", "name": "Int"}},
					},
				},
				{
					"kind": "OBJECT",
					"name": "Error",
					"fields": []map[string]interface{}{
						{"name": "message", "type": map[string]interface{}{"kind": "SCALAR", "name": "String"}},
						{"name": "timestamp", "type": map[string]interface{}{"kind": "SCALAR", "name": "String"}},
					},
				},
				{
					"kind": "OBJECT",
					"name": "Mutation",
					"fields": []map[string]interface{}{
						{
							"name": "setValue",
							"args": []map[string]interface{}{
								{"name": "variableId", "type": map[string]interface{}{"kind": "NON_NULL", "ofType": map[string]interface{}{"kind": "SCALAR", "name": "String"}}},
								{"name": "value", "type": map[string]interface{}{"kind": "NON_NULL", "ofType": map[string]interface{}{"kind": "SCALAR", "name": "String"}}},
							},
							"type": map[string]interface{}{"kind": "OBJECT", "name": "PLC"},
						},
					},
				},
			},
		},
	}

	return map[string]interface{}{"data": schema}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func formatError(err *plc.ErrorInfo) interface{} {
	if err == nil {
		return nil
	}
	return map[string]interface{}{
		"message":   err.Message,
		"timestamp": err.Timestamp.Format(time.RFC3339),
	}
}

const graphqlPlayground = `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>PLCGO GraphQL Playground</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/graphiql@3/graphiql.min.css" />
</head>
<body style="margin: 0;">
  <div id="graphiql" style="height: 100vh;"></div>
  <script crossorigin src="https://unpkg.com/react@18/umd/react.production.min.js"></script>
  <script crossorigin src="https://unpkg.com/react-dom@18/umd/react-dom.production.min.js"></script>
  <script src="https://cdn.jsdelivr.net/npm/graphiql@3/graphiql.min.js"></script>
  <script>
    const fetcher = GraphiQL.createFetcher({ url: '/graphql' });
    ReactDOM.createRoot(document.getElementById('graphiql')).render(
      React.createElement(GraphiQL, { fetcher: fetcher })
    );
  </script>
</body>
</html>`
