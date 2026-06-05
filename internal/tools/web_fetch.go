package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

type WebFetchTool struct{}

func (w WebFetchTool) Name() string {
	return "web_fetch"
}

func (w WebFetchTool) Description() string {
	return "Fetches and reads the text content of a web page. Use for reading documentation, API references, error lookups, academic pages, GitHub READMEs, or any URL the user provides."
}

func (w WebFetchTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {"type": "string", "description": "URL to fetch"}
		},
		"required": ["url"]
	}`)
}

var (
	tagRegex    = regexp.MustCompile(`<[^>]*>`)
	spaceRegex  = regexp.MustCompile(`\s+`)
	scriptRegex = regexp.MustCompile(`(?is)<script.*?</script>`)
	styleRegex  = regexp.MustCompile(`(?is)<style.*?</style>`)
)

func stripHTML(html string) string {
	text := scriptRegex.ReplaceAllString(html, "")
	text = styleRegex.ReplaceAllString(text, "")
	text = tagRegex.ReplaceAllString(text, " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", `"`)
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = spaceRegex.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}

func (w WebFetchTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if params.URL == "" {
		return "url is required", nil
	}
	req, err := http.NewRequest("GET", params.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Pragma/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("failed to fetch: %s", err.Error()), nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Sprintf("HTTP %d for %s", resp.StatusCode, params.URL), nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 500000))
	if err != nil {
		return "", err
	}
	text := stripHTML(string(body))
	if len(text) > 15000 {
		text = text[:15000] + "\n\n... (truncated)"
	}
	return text, nil
}
