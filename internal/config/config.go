package config

import (
	"os"

	"github.com/BurntSushi/toml"
)

type TierConfig struct {
	Model         string  `toml:"model"`
	ProviderName  string  `toml:"provider"`
	ApiKeyVarName string  `toml:"api_key_var_name"`
	Temperature   float64 `toml:"temperature"`
	MaxTokens     int     `toml:"max_tokens"`
	Threshold     float64 `toml:"threshold"`
}

type ModelConfig struct {
	Tiers []TierConfig `toml:"tiers"`
}

type BehaviorConfig struct {
	Verbosity  string `toml:"verbosity"`
	TestPolicy string `toml:"test_policy"`
}

type ContextConfig struct {
	MaxInputTokens int    `toml:"max_input_tokens"`
	Dependency     string `toml:"dependency"`
}

type Config struct {
	Model    ModelConfig    `toml:"model"`
	Behavior BehaviorConfig `toml:"behavior"`
	Context  ContextConfig  `toml:"context"`
}

var cfg *Config

func Get() *Config {
	return cfg
}

func Load() (*Config, error) {
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
	cfg = &Config{
		Model:    model,
		Behavior: behavior,
		Context:  context,
	}
	var bytes, err = os.ReadFile(".agent/config.toml")
	if err == nil {
		if err := toml.Unmarshal(bytes, cfg); err != nil {
			return cfg, err
		}
	} else {
		var homeDir, _ = os.UserHomeDir()
		bytes, err = os.ReadFile(homeDir + "/.config/pragma/config.toml")
		if err == nil {
			if err := toml.Unmarshal(bytes, cfg); err != nil {
				return cfg, err
			}
		}
	}
	return cfg, nil
}
