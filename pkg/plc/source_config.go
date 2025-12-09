package plc

// SourceConfig is the base configuration for all sources.
type SourceConfig struct {
	ID            string     `json:"id"`
	Type          SourceType `json:"type"`
	Name          string     `json:"name"`
	Description   string     `json:"description,omitempty"`
	Enabled       bool       `json:"enabled"`
	RetryMinDelay Duration   `json:"retryMinDelay,omitempty"`
	RetryMaxDelay Duration   `json:"retryMaxDelay,omitempty"`
}
