package tui

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/AamindMandragora/pragma/internal/llm/catalog"
)

func (m *TUIModel) updateOnboarding(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(0, msg.Width), max(0, msg.Height)
		m.resize()
		return m, nil
	case tea.KeyPressMsg:
		if keyName(msg) == keyQuit {
			return m, tea.Quit
		}
		if keyName(msg) == "enter" {
			value := strings.TrimSpace(m.input.Value())
			m.input.Reset()
			switch m.onboardStep {
			case 0, 4:
				provider := "openrouter"
				switch value {
				case "2":
					provider = "openai"
				case "3":
					provider = "anthropic"
				}
				m.onboardData = map[string]string{"provider": provider, "api_key_var": catalog.APIKeyVarForProvider(provider), "default_model": catalog.DefaultModelForProvider(provider)}
				if m.onboardData["api_key_var"] == "" {
					m.onboardData["api_key_var"] = strings.ToUpper(provider) + "_API_KEY"
				}
				if m.onboardData["default_model"] == "" {
					m.onboardData["default_model"] = "gpt-5.4-mini"
				}
				m.onboardStep++
				m.input.Placeholder = "Model name [" + m.onboardData["default_model"] + "]"
			case 1, 5:
				if value == "" {
					value = m.onboardData["default_model"]
				}
				m.onboardData["model"] = value
				m.onboardStep++
				if m.onboardStep == 2 {
					m.input.Placeholder = "Paste API key (or Enter to skip)"
				} else {
					m.input.Placeholder = "Fallback cost threshold fraction [0.5]"
				}
			case 2:
				m.onboardData["api_key"], m.onboardData["threshold"] = value, "0.0"
				m.onboardTiers = append(m.onboardTiers, m.onboardData)
				m.onboardData = make(map[string]string)
				m.onboardStep = 3
				m.input.Placeholder = "y/n [n]"
			case 3:
				if strings.EqualFold(value, "y") || strings.EqualFold(value, "yes") {
					m.onboardStep = 4
					m.input.Placeholder = "Type 1, 2 or 3"
				} else {
					m.writeOnboardConfig()
					return m, tea.Quit
				}
			case 6:
				if value == "" {
					value = "0.5"
				}
				m.onboardData["threshold"] = value
				m.onboardStep = 7
				m.input.Placeholder = "Paste fallback API key (or Enter to skip)"
			case 7:
				m.onboardData["api_key"] = value
				m.onboardTiers = append(m.onboardTiers, m.onboardData)
				m.writeOnboardConfig()
				return m, tea.Quit
			}
			return m, nil
		}
		m.input.Update(msg)
	}
	return m, nil
}

func (m *TUIModel) viewOnboarding() string {
	width := max(30, m.width)
	var lines []string
	lines = append(lines, agentMessage.Render("◆ Welcome to Pragma Setup"), "")
	switch m.onboardStep {
	case 0, 4:
		lines = append(lines, fmt.Sprintf("[%d/2] Select %s LLM provider:", map[bool]int{true: 2, false: 1}[m.onboardStep == 4], fallback(map[bool]string{true: "fallback", false: "primary"}[m.onboardStep == 4], "primary")), "", "1. OpenRouter", "2. OpenAI", "3. Anthropic")
	case 1, 5:
		lines = append(lines, mutedStyle.Render("Provider: "+m.onboardData["provider"]), "", "Enter the model ID:", mutedStyle.Render("Default: "+m.onboardData["default_model"]))
	case 2:
		lines = append(lines, mutedStyle.Render("Provider: "+m.onboardData["provider"]), mutedStyle.Render("Model: "+m.onboardData["model"]), "", "Paste the primary API key:", mutedStyle.Render("Environment variable: "+m.onboardData["api_key_var"]))
	case 3:
		lines = append(lines, toolSuccess.Render("✓ Primary tier configured"), "", "Configure a secondary fallback model?", mutedStyle.Render("Type y or n (default n)"))
	case 6:
		lines = append(lines, mutedStyle.Render("Fallback provider: "+m.onboardData["provider"]), mutedStyle.Render("Fallback model: "+m.onboardData["model"]), "", "When should Pragma downgrade?", mutedStyle.Render("Enter a fraction from 0.0 to 1.0 (default 0.5)"))
	case 7:
		lines = append(lines, mutedStyle.Render("Fallback provider: "+m.onboardData["provider"]), mutedStyle.Render("Fallback model: "+m.onboardData["model"]), mutedStyle.Render("Switch point: "+m.onboardData["threshold"]), "", "Paste the fallback API key:", mutedStyle.Render("Environment variable: "+m.onboardData["api_key_var"]))
	}
	lines = append(lines, "", "> "+m.input.View(), "", subtleStyle.Render("Enter continue · Alt+C quit"))
	return m.renderPanel("ONBOARDING", lines, min(width-4, 78), max(10, m.height-4), true)
}

func (m *TUIModel) writeOnboardConfig() {
	_ = os.MkdirAll(".agent", 0755)
	var tiers strings.Builder
	for _, tier := range m.onboardTiers {
		tiers.WriteString("[[model.tiers]]\n")
		tiers.WriteString(fmt.Sprintf("model = %q\nprovider = %q\napi_key_var_name = %q\neffort = \"\"\nthreshold = %s\n\n", tier["model"], tier["provider"], tier["api_key_var"], tier["threshold"]))
	}
	config := fmt.Sprintf("%s[behavior]\nverbosity = \"minimal\"\ntest_policy = \"none\"\nmax_output_tokens = 8192\n", tiers.String())
	_ = os.WriteFile(".agent/config.toml", []byte(config), 0644)
	if file, err := os.OpenFile(".agent/.env", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
		defer file.Close()
		for _, tier := range m.onboardTiers {
			if tier["api_key"] != "" {
				_, _ = file.WriteString(fmt.Sprintf("%s=%s\n", tier["api_key_var"], tier["api_key"]))
			}
		}
	}
}
