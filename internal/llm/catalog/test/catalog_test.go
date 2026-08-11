package catalog_test

import (
	"testing"

	"github.com/AamindMandragora/pragma/internal/llm/catalog"
)

func TestResolveAndBuildBody(t *testing.T) {
	o, err := catalog.ResolveProvider("openai")
	if err != nil {
		t.Fatal(err)
	}
	body := o.BuildBody(map[string]interface{}{
		"model":      "gpt-5.4-mini",
		"input":      []any{},
		"stream":     true,
		"max_tokens": 4096,
		"reasoning": map[string]string{
			"effort": "high",
		},
		"temperature":  0.7,
		"instructions": "hi",
	})
	if _, ok := body["temperature"]; ok {
		t.Fatal("temperature should be omitted for openai responses")
	}
	if body["max_output_tokens"] != 4096 {
		t.Fatalf("expected max_output_tokens, got %#v", body["max_output_tokens"])
	}
	if body["reasoning"] == nil {
		t.Fatal("expected reasoning to be included")
	}
	if effort, ok := body["reasoning"].(map[string]string)["effort"]; !ok || effort != "high" {
		t.Fatalf("unexpected reasoning effort: %#v", body["reasoning"])
	}
	if o.Event("text_delta") != "response.output_text.delta" {
		t.Fatalf("unexpected text_delta event: %q", o.Event("text_delta"))
	}
	if o.Event("text_done") != "response.output_text.done" {
		t.Fatalf("unexpected text_done event: %q", o.Event("text_done"))
	}

	a, err := catalog.ResolveProvider("anthropic")
	if err != nil {
		t.Fatal(err)
	}
	abody := a.BuildBody(map[string]interface{}{
		"model":      "claude-sonnet-4-5",
		"messages":   []any{},
		"stream":     true,
		"max_tokens": 4096,
		"output_config": map[string]string{
			"effort": "high",
		},
		"temperature": 0.7,
		"system":      "sys",
	})
	if abody["temperature"] != 0.7 {
		t.Fatalf("anthropic should keep temperature, got %#v", abody["temperature"])
	}
	if abody["max_tokens"] != 4096 {
		t.Fatalf("expected max_tokens, got %#v", abody["max_tokens"])
	}
	outputConfig, ok := abody["output_config"].(map[string]string)
	if !ok || outputConfig["effort"] != "high" {
		t.Fatalf("unexpected output_config effort: %#v", abody["output_config"])
	}

	in, cached, out, ok := catalog.Price("gpt-5.4-mini")
	if !ok || in != 0.75 || cached != 0.075 || out != 4.50 {
		t.Fatalf("unexpected price: ok=%v in=%v cached=%v out=%v", ok, in, cached, out)
	}
	if catalog.DefaultModelForProvider("openrouter") == "" {
		t.Fatal("openrouter default model missing")
	}
	if catalog.APIKeyVarForProvider("anthropic") != "ANTHROPIC_API_KEY" {
		t.Fatal("anthropic api key var wrong")
	}
	if catalog.DefaultModelForProvider("anthropic") != "claude-sonnet-5" {
		t.Fatal("anthropic default model wrong")
	}
	in56, cached56, out56, ok56 := catalog.Price("gpt-5.6-terra")
	if !ok56 || in56 != 2.00 || cached56 != 0.20 || out56 != 12.00 {
		t.Fatalf("unexpected gpt-5.6-terra price: ok=%v in=%v cached=%v out=%v", ok56, in56, cached56, out56)
	}
	inOpus, _, outOpus, okOpus := catalog.Price("claude-opus-5")
	if !okOpus || inOpus != 5.00 || outOpus != 25.00 {
		t.Fatalf("unexpected claude-opus-5 price: ok=%v in=%v out=%v", okOpus, inOpus, outOpus)
	}
}
