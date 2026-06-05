package config

import (
	"os"

	"github.com/BurntSushi/toml"
)

type ModelConfig struct {
	Provider      string `toml:"provider"`
	ModelName     string `toml:"model_name"`
	ApiKeyVarName string `toml:"api_key_var_name"`
	ToolMode      string `toml:"tool_mode"`
}

type BehaviorConfig struct {
	Verbosity       string `toml:"verbosity"`
	TestPolicy      string `toml:"test_policy"`
	MaxOutputTokens int    `toml:"max_output_tokens"`
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

func Load(path string) (*Config, error) {
	var model = ModelConfig{
		Provider:      "openai",
		ModelName:     "gpt-4.1-mini",
		ApiKeyVarName: "OPENAI_API_KEY",
	}
	var behavior = BehaviorConfig{
		Verbosity:       "minimal",
		TestPolicy:      "none",
		MaxOutputTokens: 4096,
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
	if path != "" {
		var bytes, err = os.ReadFile(path)
		if err != nil {
			return cfg, err
		}
		if err := toml.Unmarshal(bytes, cfg); err != nil {
			return cfg, err
		}
	} else {
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
	}
	return cfg, nil
}
