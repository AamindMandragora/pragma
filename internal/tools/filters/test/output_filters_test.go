package filters_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AamindMandragora/pragma/internal/llm"
	"github.com/AamindMandragora/pragma/internal/tools"
	"github.com/AamindMandragora/pragma/internal/tools/filters"
)

type outputFilterTestTool struct {
	output string
	args   json.RawMessage
}

func (t *outputFilterTestTool) Name() string        { return "emit_output" }
func (t *outputFilterTestTool) Description() string { return "test output source" }
func (t *outputFilterTestTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t *outputFilterTestTool) Execute(args json.RawMessage) (string, error) {
	t.args = append(t.args[:0], args...)
	return t.output, nil
}

func testFilterRegistry(source *outputFilterTestTool) *tools.Registry {
	registry := tools.NewRegistry()
	registry.Register(source)
	filters.RegisterBuiltinOutputFilters(registry, nil)
	return registry
}

func TestToolOutputIsCompleteWithoutFilters(t *testing.T) {
	source := &outputFilterTestTool{output: "first\nsecond\nthird\nfourth"}
	result, err := testFilterRegistry(source).Dispatch("emit_output", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if result != source.output {
		t.Fatalf("output was changed without filters: %q", result)
	}
}

func TestToolOutputFiltersComposeInOrder(t *testing.T) {
	source := &outputFilterTestTool{output: "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"}
	registry := testFilterRegistry(source)
	args := json.RawMessage(`{"filters":[{"name":"head","args":{"n":6}},{"name":"tail","args":{"n":2}}]}`)
	result, err := registry.Dispatch("emit_output", args)
	if err != nil {
		t.Fatal(err)
	}
	if result != "5\n6\n" {
		t.Fatalf("unexpected composed output: %q", result)
	}
	if strings.Contains(string(source.args), "filters") {
		t.Fatalf("composition metadata leaked into source args: %s", source.args)
	}
}

func TestOutputFiltersAreCallableTools(t *testing.T) {
	registry := testFilterRegistry(&outputFilterTestTool{})
	result, err := registry.Dispatch("find_issues", json.RawMessage(`{"text":"ok\nWARNING: retrying\ndone"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result != "[line 2] WARNING: retrying" {
		t.Fatalf("unexpected direct filter result: %q", result)
	}
}

func TestFindIssuesIncludesContext(t *testing.T) {
	registry := testFilterRegistry(&outputFilterTestTool{})
	result, err := registry.Dispatch("find_issues", json.RawMessage(`{"text":"before\nERROR: broken\nafter\nlast","context":1}`))
	if err != nil {
		t.Fatal(err)
	}
	expected := "[line 1] before\n[line 2] ERROR: broken\n[line 3] after"
	if result != expected {
		t.Fatalf("unexpected issue context: %q", result)
	}
}

func TestGrepIgnoreCaseAndLineNumbers(t *testing.T) {
	registry := testFilterRegistry(&outputFilterTestTool{})
	result, err := registry.Dispatch("grep", json.RawMessage(`{"text":"alpha\nBeta\ngamma","pattern":"beta"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result != "[line 2] Beta" {
		t.Fatalf("unexpected case-insensitive grep result: %q", result)
	}
}

func TestLineAndJSONFilters(t *testing.T) {
	registry := testFilterRegistry(&outputFilterTestTool{})
	lineResult, err := registry.Dispatch("line_range", json.RawMessage(`{"text":"a\nb\nc\nd","start_line":2,"end_line":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if lineResult != "b\nc\n" {
		t.Fatalf("unexpected line range: %q", lineResult)
	}
	jsonResult, err := registry.Dispatch("json_pretty", json.RawMessage(`{"text":"{\"ok\":true,\"items\":[1,2]}"}`))
	if err != nil {
		t.Fatal(err)
	}
	if jsonResult != "{\n  \"ok\": true,\n  \"items\": [\n    1,\n    2\n  ]\n}" {
		t.Fatalf("unexpected pretty JSON: %q", jsonResult)
	}
}

func TestFilterSchemaAdvertisesComposition(t *testing.T) {
	registry := testFilterRegistry(&outputFilterTestTool{})
	var schema map[string]interface{}
	for _, tool := range registry.List() {
		if tool.Name == "emit_output" {
			if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("tool schema has no properties")
	}
	if _, ok := properties["filters"]; !ok {
		t.Fatal("tool schema does not advertise output filters")
	}
}

type summaryTestProvider struct {
	seen string
}

func (p *summaryTestProvider) Chat(_ context.Context, messages []llm.Message, _ []llm.ToolDef, _ llm.Model) (<-chan llm.StreamEvent, error) {
	for _, message := range messages {
		p.seen += message.Content
	}
	ch := make(chan llm.StreamEvent, 2)
	ch <- llm.StreamEvent{Type: "text", Text: "private summary"}
	ch <- llm.StreamEvent{Type: "done"}
	close(ch)
	return ch, nil
}

func (p *summaryTestProvider) GetName() string { return "summary-test" }

func TestSummarizeOutputIsOnlyTheSummaryAbstraction(t *testing.T) {
	provider := &summaryTestProvider{}
	model := &llm.Model{Name: "test", MaxTokens: 1024, Provider: provider}
	registry := tools.NewRegistry()
	source := &outputFilterTestTool{output: "package example\n\nfunc Important() {}\n"}
	registry.Register(source)
	filters.RegisterBuiltinOutputFilters(registry, model)

	result, err := registry.Dispatch("emit_output", json.RawMessage(`{"filters":[{"name":"summarize_output","args":{"focus":"public functions"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if result != "private summary" {
		t.Fatalf("unexpected summary: %q", result)
	}
	if !strings.Contains(provider.seen, source.output) {
		t.Fatal("summary filter did not receive complete tool output")
	}
}
