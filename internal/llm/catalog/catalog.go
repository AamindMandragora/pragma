package catalog

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// ProtocolFile is one embedded catalog file (openai-compatible or anthropic-compatible).
type ProtocolFile struct {
	Protocol  string              `json:"protocol"`
	Defaults  Defaults            `json:"defaults"`
	Providers map[string]Provider `json:"providers"`
	Models    []ModelEntry        `json:"models"`
}

// Defaults are shared by every provider in the file unless overridden.
type Defaults struct {
	EndpointPath string            `json:"endpoint_path"`
	AuthHeader   string            `json:"auth_header"`
	AuthPrefix   string            `json:"auth_prefix"`
	Headers      map[string]string `json:"headers"`
	ToolMode     string            `json:"tool_mode"`
	Request      RequestFields     `json:"request"`
	StreamEvents map[string]string `json:"stream_events"`
}

// Provider is a named API endpoint that inherits Defaults.
type Provider struct {
	BaseURL      string            `json:"base_url"`
	APIKeyVar    string            `json:"api_key_var"`
	DefaultModel string            `json:"default_model"`
	EndpointPath string            `json:"endpoint_path,omitempty"`
	AuthHeader   string            `json:"auth_header,omitempty"`
	AuthPrefix   *string           `json:"auth_prefix,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	ToolMode     string            `json:"tool_mode,omitempty"`
	Request      RequestFields     `json:"request,omitempty"`
	StreamEvents map[string]string `json:"stream_events,omitempty"`
}

// ModelEntry describes a model id, which providers serve it, and pricing.
type ModelEntry struct {
	ID         string          `json:"id"`
	Providers  []string        `json:"providers"`
	Pricing    Pricing         `json:"pricing"`
	Benchmarks json.RawMessage `json:"benchmarks"`
	// NoTemperature marks models that reject the temperature parameter outright.
	// defaults to false so older models keep sending it
	NoTemperature bool `json:"no_temperature"`
}

// Pricing is USD per 1M tokens.
type Pricing struct {
	Input       float64 `json:"input"`
	CachedInput float64 `json:"cached_input"`
	Output      float64 `json:"output"`
}

// RequestFields maps logical field names to wire names.
// A JSON null value means the field must be omitted from the request body.
type RequestFields map[string]json.RawMessage

// WireName returns the API field name for a logical key, or ok=false if omitted/missing.
func (r RequestFields) WireName(logical string) (string, bool) {
	raw, ok := r[logical]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var name string
	if err := json.Unmarshal(raw, &name); err != nil || name == "" {
		return "", false
	}
	return name, true
}

// ResolvedProvider is defaults deep-merged with a provider entry.
type ResolvedProvider struct {
	Name         string
	Protocol     string
	BaseURL      string
	APIKeyVar    string
	DefaultModel string
	EndpointPath string
	AuthHeader   string
	AuthPrefix   string
	Headers      map[string]string
	ToolMode     string
	Request      RequestFields
	StreamEvents map[string]string
}

// Event returns the wire event type for a logical stream event key.
func (r ResolvedProvider) Event(logical string) string {
	if r.StreamEvents == nil {
		return ""
	}
	return r.StreamEvents[logical]
}

// BuildBody maps logical request values onto wire field names, skipping omitted fields.
func (r ResolvedProvider) BuildBody(logical map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(logical))
	for key, value := range logical {
		if value == nil {
			continue
		}
		wire, ok := r.Request.WireName(key)
		if !ok {
			continue
		}
		// skip empty optional strings (e.g. instructions/system when unset)
		if s, isStr := value.(string); isStr && s == "" {
			continue
		}
		out[wire] = value
	}
	return out
}

var (
	loadOnce sync.Once
	loadErr  error
	files    []ProtocolFile
	byProv   map[string]ResolvedProvider
	byModel  map[string]ModelEntry
)

// Load parses embedded catalog JSON once.
func Load() error {
	loadOnce.Do(func() {
		byProv = map[string]ResolvedProvider{}
		byModel = map[string]ModelEntry{}
		entries, err := dataFS.ReadDir("data")
		if err != nil {
			loadErr = err
			return
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			raw, err := dataFS.ReadFile("data/" + e.Name())
			if err != nil {
				loadErr = err
				return
			}
			var pf ProtocolFile
			if err := json.Unmarshal(raw, &pf); err != nil {
				loadErr = fmt.Errorf("catalog %s: %w", e.Name(), err)
				return
			}
			files = append(files, pf)
			for name, p := range pf.Providers {
				byProv[name] = resolve(pf.Protocol, name, pf.Defaults, p)
			}
			for _, m := range pf.Models {
				byModel[m.ID] = m
			}
		}
	})
	return loadErr
}

func resolve(protocol, name string, d Defaults, p Provider) ResolvedProvider {
	headers := map[string]string{}
	for k, v := range d.Headers {
		headers[k] = v
	}
	for k, v := range p.Headers {
		headers[k] = v
	}

	request := RequestFields{}
	for k, v := range d.Request {
		request[k] = v
	}
	for k, v := range p.Request {
		request[k] = v
	}

	events := map[string]string{}
	for k, v := range d.StreamEvents {
		events[k] = v
	}
	for k, v := range p.StreamEvents {
		events[k] = v
	}

	endpoint := d.EndpointPath
	if p.EndpointPath != "" {
		endpoint = p.EndpointPath
	}
	authHeader := d.AuthHeader
	if p.AuthHeader != "" {
		authHeader = p.AuthHeader
	}
	authPrefix := d.AuthPrefix
	if p.AuthPrefix != nil {
		authPrefix = *p.AuthPrefix
	}
	toolMode := d.ToolMode
	if p.ToolMode != "" {
		toolMode = p.ToolMode
	}

	return ResolvedProvider{
		Name:         name,
		Protocol:     protocol,
		BaseURL:      p.BaseURL,
		APIKeyVar:    p.APIKeyVar,
		DefaultModel: p.DefaultModel,
		EndpointPath: endpoint,
		AuthHeader:   authHeader,
		AuthPrefix:   authPrefix,
		Headers:      headers,
		ToolMode:     toolMode,
		Request:      request,
		StreamEvents: events,
	}
}

// ResolveProvider finds a provider by name across all catalog files.
func ResolveProvider(name string) (ResolvedProvider, error) {
	if err := Load(); err != nil {
		return ResolvedProvider{}, err
	}
	p, ok := byProv[name]
	if !ok {
		return ResolvedProvider{}, fmt.Errorf("unknown provider %q", name)
	}
	return p, nil
}

// LookupModel finds a model by exact id, then prefix/suffix match.
func LookupModel(id string) (ModelEntry, bool) {
	if err := Load(); err != nil {
		return ModelEntry{}, false
	}
	if m, ok := byModel[id]; ok {
		return m, true
	}
	for key, m := range byModel {
		if strings.HasPrefix(id, key) || strings.HasSuffix(id, key) {
			return m, true
		}
	}
	return ModelEntry{}, false
}

// Price returns input / cached-input / output USD-per-1M for a model id.
func Price(model string) (input, cached, output float64, ok bool) {
	m, found := LookupModel(model)
	if !found {
		return 0, 0, 0, false
	}
	return m.Pricing.Input, m.Pricing.CachedInput, m.Pricing.Output, true
}

// DefaultModelForProvider returns the catalog default_model for a provider, if set.
func DefaultModelForProvider(name string) string {
	p, err := ResolveProvider(name)
	if err != nil {
		return ""
	}
	return p.DefaultModel
}

// APIKeyVarForProvider returns the catalog api_key_var for a provider.
func APIKeyVarForProvider(name string) string {
	p, err := ResolveProvider(name)
	if err != nil {
		return ""
	}
	return p.APIKeyVar
}
