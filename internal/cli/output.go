package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// mcpCallResult is the shape returned by tools/call.
type mcpCallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// mcpResourceResult is the shape returned by resources/read.
type mcpResourceResult struct {
	Contents []struct {
		URI      string `json:"uri"`
		MimeType string `json:"mimeType"`
		Text     string `json:"text"`
	} `json:"contents"`
}

// renderJSON renders v as compact or indented JSON. Numbers are preserved via
// json.Number on the decode side.
func renderJSON(v any, pretty bool) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if pretty {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// parseJSONValue decodes text preserving number literals. It also rejects
// trailing garbage so a plain string such as "abc" is not mistaken for JSON.
func parseJSONValue(text string) (any, bool) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, false
	}
	return v, true
}

// formatContent pretty-prints embedded JSON, falling back to raw text.
func formatContent(text string, pretty bool) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return text
	}
	c := trimmed[0]
	if c != '{' && c != '[' {
		return text
	}
	v, ok := parseJSONValue(trimmed)
	if !ok {
		return text
	}
	rendered, err := renderJSON(v, pretty)
	if err != nil {
		return text
	}
	return rendered
}

// renderCallResult unwraps result.content[*].text and returns the rendered
// text plus whether the tool reported an error.
func renderCallResult(raw json.RawMessage, pretty bool) (string, bool, error) {
	var result mcpCallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", false, fmt.Errorf("decode tool result: %w", err)
	}
	if len(result.Content) == 0 {
		// Some tools return a bare object; render it directly.
		v, ok := parseJSONValue(string(raw))
		if !ok {
			return string(raw), result.IsError, nil
		}
		rendered, err := renderJSON(v, pretty)
		if err != nil {
			return "", result.IsError, err
		}
		return rendered, result.IsError, nil
	}

	parts := make([]string, 0, len(result.Content))
	for _, item := range result.Content {
		if item.Text != "" {
			parts = append(parts, item.Text)
		}
	}
	joined := strings.Join(parts, "\n")
	return formatContent(joined, pretty), result.IsError, nil
}

// renderResourceResult renders resources/read output.
func renderResourceResult(raw json.RawMessage, pretty bool) (string, error) {
	var result mcpResourceResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("decode resource result: %w", err)
	}
	if len(result.Contents) == 1 && result.Contents[0].Text != "" {
		return formatContent(result.Contents[0].Text, pretty), nil
	}
	v, ok := parseJSONValue(string(raw))
	if !ok {
		return string(raw), nil
	}
	return renderJSON(v, pretty)
}

// truncateBytes caps s at max bytes without splitting a UTF-8 rune. max <= 0
// disables the cap.
func truncateBytes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	removed := len(s) - cut
	return s[:cut] + fmt.Sprintf("\n...[truncated %d bytes]", removed)
}

// writeText writes s followed by exactly one newline.
func writeText(w io.Writer, s string) error {
	if _, err := io.WriteString(w, strings.TrimRight(s, "\r\n")+"\n"); err != nil {
		return err
	}
	return nil
}

// printJSONValue writes v as JSON.
func printJSONValue(w io.Writer, v any, pretty bool) error {
	s, err := renderJSON(v, pretty)
	if err != nil {
		return err
	}
	return writeText(w, s)
}
