package config

import (
	"os"

	"github.com/BurntSushi/toml"
)

// each tier holds a model, its provider, the name of the variable holding the api key, the temperature, the max tokens, and the minimum budget threshold that needs to be spent before we switch to using it
type TierConfig struct {
	Model         string  `toml:"model"`
	ProviderName  string  `toml:"provider"`
	ApiKeyVarName string  `toml:"api_key_var_name"`
	Temperature   float64 `toml:"temperature"`
	MaxTokens     int     `toml:"max_tokens"`
	Threshold     float64 `toml:"threshold"`
}

// our model config simply holds an array of tiers ordered from most to least expensive
type ModelConfig struct {
	Tiers []TierConfig `toml:"tiers"`
}

// holds the desired output behavior of the model (UNUSED)
type BehaviorConfig struct {
	Verbosity  string `toml:"verbosity"`
	TestPolicy string `toml:"test_policy"`
}

// holds the desired input behavior of the model (UNUSED)
type ContextConfig struct {
	MaxInputTokens int    `toml:"max_input_tokens"`
	Dependency     string `toml:"dependency"`
}

// holds all three config structs
type Config struct {
	Model    ModelConfig    `toml:"model"`
	Behavior BehaviorConfig `toml:"behavior"`
	Context  ContextConfig  `toml:"context"`
}

// local pointer to config
var cfg *Config

// config getter
func Get() *Config {
	return cfg
}

// loads the config from .agents/config.toml
func Load() (*Config, error) {
	// sets up default values for tier, model, behavior, context configs
	var tier = TierConfig{
		Model:         "gpt-5.4-mini",
		ProviderName:  "openai",
		ApiKeyVarName: "OPENAI_API_KEY",
		Temperature:   0.7,
		Threshold:     0,
	}
	var model = ModelConfig{
		Tiers: []TierConfig{tier},
	}
	var behavior = BehaviorConfig{
		Verbosity:  "minimal",
		TestPolicy: "none",
	}
	var context = ContextConfig{
		MaxInputTokens: 4096,
		Dependency:     "main",
	}
	// creates a config using the defaults
	cfg = &Config{
		Model:    model,
		Behavior: behavior,
		Context:  context,
	}
	// attempts to read config from .agent/config.toml
	var bytes, err = os.ReadFile(".agent/config.toml")
	if err == nil {
		// tries to convert the raw bytes to the struct, returns error if fails
		if err := toml.Unmarshal(bytes, cfg); err != nil {
			return cfg, err
		}
	}
	// returns the config and nil error
	return cfg, nil
}
