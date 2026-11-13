package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func TestRenderCallResultJSON(t *testing.T) {
	in := `{"content":[{"type":"text","text":"{\"addr\":\"main\",\"code\":\"int main(){}\"}"}]}`
	text, isErr, err := renderCallResult(raw(in), false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if isErr {
		t.Fatal("unexpected isError")
	}
	if text != `{"addr":"main","code":"int main(){}"}` {
		t.Fatalf("got %q", text)
	}
}

func TestRenderCallResultPretty(t *testing.T) {
	in := `{"content":[{"type":"text","text":"{\"a\":1}"}]}`
	text, _, err := renderCallResult(raw(in), true)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(text, "\n  \"a\": 1") {
		t.Fatalf("expected indented JSON, got %q", text)
	}
}

func TestRenderCallResultPreservesBigNumbers(t *testing.T) {
	const big = "18446744073709551615"
	in := `{"content":[{"type":"text","text":"{\"addr\":` + big + `}"}]}`
	text, _, err := renderCallResult(raw(in), false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(text, big) {
		t.Fatalf("large integer lost precision: %q", text)
	}
}

func TestRenderCallResultIsError(t *testing.T) {
	in := `{"content":[{"type":"text","text":"boom"}],"isError":true}`
	text, isErr, err := renderCallResult(raw(in), false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !isErr || text != "boom" {
		t.Fatalf("got text=%q isErr=%v", text, isErr)
	}
}

func TestRenderPlainText(t *testing.T) {
	in := `{"content":[{"type":"text","text":"plain output"}]}`
	text, _, err := renderCallResult(raw(in), false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if text != "plain output" {
		t.Fatalf("got %q", text)
	}
}

func TestRenderResourceResult(t *testing.T) {
	in := `{"contents":[{"uri":"ida://idb/metadata","mimeType":"application/json","text":"{\"arch\":\"x86_64\"}"}]}`
	text, err := renderResourceResult(raw(in), false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if text != `{"arch":"x86_64"}` {
		t.Fatalf("got %q", text)
	}
}

func TestTruncateBytesKeepsUTF8(t *testing.T) {
	s := "abc中文字"
	for max := 0; max < len(s); max++ {
		got := truncateBytes(s, max)
		if !utf8.ValidString(got) {
			t.Fatalf("max=%d produced invalid UTF-8: %q", max, got)
		}
	}
	if got := truncateBytes("short", 100); got != "short" {
		t.Fatalf("unexpected truncation: %q", got)
	}
	if got := truncateBytes("abcdef", 3); !strings.Contains(got, "truncated 3 bytes") {
		t.Fatalf("missing marker: %q", got)
	}
}

func TestParseJSONValueRejectsTrailingGarbage(t *testing.T) {
	if _, ok := parseJSONValue(`{"a":1} trailing`); ok {
		t.Fatal("trailing garbage accepted")
	}
	if v, ok := parseJSONValue(`{"a":1}`); !ok || v == nil {
		t.Fatal("valid JSON rejected")
	}
}
