package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestLoginTypeFromAuthMetadata(t *testing.T) {
	tests := []struct {
		name string
		meta map[string]any
		want string
	}{
		{"string value", map[string]any{"loginType": "PIN_JD_CLOUD"}, "PIN_JD_CLOUD"},
		{"empty string falls back", map[string]any{"loginType": ""}, "N_PIN_PC"},
		{"missing key falls back", map[string]any{}, "N_PIN_PC"},
		{"wrong type falls back", map[string]any{"loginType": 123}, "N_PIN_PC"},
		{"nil metadata", nil, "N_PIN_PC"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := loginTypeFromAuthMetadata(tt.meta); got != tt.want {
				t.Errorf("loginTypeFromAuthMetadata() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSimpleAtoi(t *testing.T) {
	tests := []struct {
		input   string
		wantVal int
		wantOK  bool
	}{
		{"0", 0, true},
		{"123", 123, true},
		{"8317", 8317, true},
		{"", 0, true},
		{"12a3", 0, false},
		{" 123", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			val, ok := simpleAtoi(tt.input)
			if val != tt.wantVal || ok != tt.wantOK {
				t.Errorf("simpleAtoi(%q) = (%d, %v), want (%d, %v)", tt.input, val, ok, tt.wantVal, tt.wantOK)
			}
		})
	}
}

func TestParseSimpleURL(t *testing.T) {
	tests := []struct {
		raw   string
		scheme string
		host   string
		port   string
		path   string
		wantErr bool
	}{
		{"http://127.0.0.1:8317/callback", "http", "127.0.0.1", "8317", "/callback", false},
		{"https://example.com/path", "https", "example.com", "", "/path", false},
		{"https://example.com", "https", "example.com", "", "", false},
		{"no-scheme", "", "", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			u, err := parseSimpleURL(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseSimpleURL() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if u.Scheme != tt.scheme || u.Host != tt.host || u.Port != tt.port || u.Path != tt.path {
				t.Errorf("parseSimpleURL() = {%s, %s, %s, %s}, want {%s, %s, %s, %s}",
					u.Scheme, u.Host, u.Port, u.Path,
					tt.scheme, tt.host, tt.port, tt.path)
			}
		})
	}
}

func TestExtractPortFromURL(t *testing.T) {
	tests := []struct {
		raw  string
		want int
	}{
		{"http://127.0.0.1:8317/callback", 8317},
		{"http://localhost:9999/", 9999},
		{"https://example.com/path", 0},
		{"", 0},
		{"127.0.0.1:4321/foo", 4321},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := extractPortFromURL(tt.raw); got != tt.want {
				t.Errorf("extractPortFromURL(%q) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}

func TestDecompressGzip(t *testing.T) {
	t.Run("gzip content", func(t *testing.T) {
		original := []byte("hello gzip world")
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		gw.Write(original)
		gw.Close()

		headers := map[string][]string{"Content-Encoding": {"gzip"}}
		result := decompressGzip(buf.Bytes(), headers)
		if !bytes.Equal(result, original) {
			t.Errorf("decompressGzip() = %q, want %q", result, original)
		}
	})

	t.Run("non-gzip passthrough", func(t *testing.T) {
		data := []byte("plain text")
		headers := map[string][]string{"Content-Encoding": {"identity"}}
		result := decompressGzip(data, headers)
		if !bytes.Equal(result, data) {
			t.Errorf("decompressGzip() = %q, want %q", result, data)
		}
	})

	t.Run("no encoding header passthrough", func(t *testing.T) {
		data := []byte("plain text")
		result := decompressGzip(data, nil)
		if !bytes.Equal(result, data) {
			t.Errorf("decompressGzip() = %q, want %q", result, data)
		}
	})

	t.Run("invalid gzip data passthrough", func(t *testing.T) {
		data := []byte("not actually gzip")
		headers := map[string][]string{"Content-Encoding": {"gzip"}}
		result := decompressGzip(data, headers)
		if !bytes.Equal(result, data) {
			t.Errorf("decompressGzip() on invalid data = %q, want original %q", result, data)
		}
	})
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"single line", "hello\n", []string{"hello"}},
		{"multiple lines", "line1\nline2\nline3\n", []string{"line1", "line2", "line3"}},
		{"empty input", "", nil},
		{"CRLF", "line1\r\nline2\r\n", []string{"line1", "line2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitLines([]byte(tt.input))
			if len(got) != len(tt.want) {
				t.Fatalf("splitLines() got %d lines, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("line[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestInjectPayloadFields(t *testing.T) {
	t.Run("sets required fields", func(t *testing.T) {
		input := map[string]any{"messages": []any{"hello"}}
		inputJSON, _ := json.Marshal(input)

		result := injectPayloadFields(inputJSON, "GLM-5.1", "user123")

		var out map[string]any
		if err := json.Unmarshal(result, &out); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}

		if out["model"] != "GLM-5.1" {
			t.Errorf("model = %v, want GLM-5.1", out["model"])
		}
		if out["tenant"] != "JOYCODE" {
			t.Errorf("tenant = %v, want JOYCODE", out["tenant"])
		}
		if out["userId"] != "user123" {
			t.Errorf("userId = %v, want user123", out["userId"])
		}
		if out["client"] != "JoyCode" {
			t.Errorf("client = %v, want JoyCode", out["client"])
		}
		if out["clientVersion"] != JCClientVersion {
			t.Errorf("clientVersion = %v, want %s", out["clientVersion"], JCClientVersion)
		}
		for _, key := range []string{"sessionId", "chatId", "requestId"} {
			if _, ok := out[key]; !ok {
				t.Errorf("missing key %q", key)
			}
		}
	})

	t.Run("reasoning model gets default thinking disabled", func(t *testing.T) {
		input := map[string]any{"messages": []any{"hello"}}
		inputJSON, _ := json.Marshal(input)

		for _, model := range []string{"GLM-5.1", "Kimi-K2.6", "MiniMax-M2.7"} {
			result := injectPayloadFields(inputJSON, model, "")
			var out map[string]any
			json.Unmarshal(result, &out)
			thinking, _ := out["thinking"].(map[string]any)
			if thinking["type"] != "disabled" {
				t.Errorf("model %s: thinking = %v, want disabled", model, thinking)
			}
		}
	})

	t.Run("reasoning model preserves existing thinking", func(t *testing.T) {
		input := map[string]any{
			"messages": []any{"hello"},
			"thinking": map[string]any{"type": "enabled", "budget_tokens": 5000.0},
		}
		inputJSON, _ := json.Marshal(input)

		result := injectPayloadFields(inputJSON, "GLM-5.1", "")
		var out map[string]any
		json.Unmarshal(result, &out)
		thinking, _ := out["thinking"].(map[string]any)
		if thinking["type"] != "enabled" {
			t.Errorf("existing thinking should be preserved, got %v", thinking)
		}
	})

	t.Run("non-reasoning model always gets thinking disabled", func(t *testing.T) {
		input := map[string]any{
			"messages": []any{"hello"},
			"thinking": map[string]any{"type": "enabled"},
		}
		inputJSON, _ := json.Marshal(input)

		result := injectPayloadFields(inputJSON, "JoyAI-Code", "")
		var out map[string]any
		json.Unmarshal(result, &out)
		thinking, _ := out["thinking"].(map[string]any)
		if thinking["type"] != "disabled" {
			t.Errorf("non-reasoning model thinking = %v, want disabled", thinking)
		}
	})

	t.Run("preserves existing sessionId/chatId/requestId", func(t *testing.T) {
		input := map[string]any{
			"messages":  []any{"hello"},
			"sessionId": "keep-this",
		}
		inputJSON, _ := json.Marshal(input)

		result := injectPayloadFields(inputJSON, "JoyAI-Code", "")
		var out map[string]any
		json.Unmarshal(result, &out)
		if out["sessionId"] != "keep-this" {
			t.Errorf("sessionId = %v, want keep-this", out["sessionId"])
		}
	})

	t.Run("invalid JSON passthrough", func(t *testing.T) {
		bad := []byte("{not valid json")
		result := injectPayloadFields(bad, "JoyAI-Code", "")
		if !bytes.Equal(result, bad) {
			t.Error("invalid JSON should be returned unchanged")
		}
	})
}

func TestBuildJCHeaders(t *testing.T) {
	headers := buildJCHeaders("my-ptkey", "N_PIN_PC")

	ptKey, _ := headers["ptKey"].([]string)
	if len(ptKey) == 0 || ptKey[0] != "my-ptkey" {
		t.Errorf("ptKey header = %v, want [my-ptkey]", ptKey)
	}

	loginType, _ := headers["loginType"].([]string)
	if len(loginType) == 0 || loginType[0] != "N_PIN_PC" {
		t.Errorf("loginType header = %v, want [N_PIN_PC]", loginType)
	}

	requiredHeaders := []string{"Content-Type", "User-Agent", "Accept", "Accept-Encoding", "Connection"}
	for _, h := range requiredHeaders {
		if _, ok := headers[h]; !ok {
			t.Errorf("missing required header %q", h)
		}
	}
}

func TestHandleAuthParse(t *testing.T) {
	t.Run("valid joycode auth file", func(t *testing.T) {
		raw := map[string]any{
			"type":         "joycode",
			"ptKey":        "test-pt-key",
			"userId":       "user1",
			"tenant":       "JD",
			"orgFullName":  "Org",
			"loginType":    "N_PIN_PC",
		}
		rawJSON, _ := json.Marshal(raw)

		req := authParseRequest{
			Provider: "joycode",
			FileName: "joycode-user1.json",
			RawJSON:  base64.StdEncoding.EncodeToString(rawJSON),
		}
		reqBody, _ := json.Marshal(req)

		result, err := handleAuthParse(reqBody)
		if err != nil {
			t.Fatalf("handleAuthParse() error = %v", err)
		}

		var env abiEnvelope
		json.Unmarshal(result, &env)
		if !env.OK {
			t.Fatalf("expected OK response, got error: %v", env.Error)
		}

		var body map[string]any
		json.Unmarshal(env.Result, &body)

		handled, _ := body["Handled"].(bool)
		if !handled {
			t.Error("expected Handled=true")
		}
	})

	t.Run("non-joycode type returns Handled=false", func(t *testing.T) {
		raw := map[string]any{"type": "other", "ptKey": "key"}
		rawJSON, _ := json.Marshal(raw)

		req := authParseRequest{
			RawJSON: base64.StdEncoding.EncodeToString(rawJSON),
		}
		reqBody, _ := json.Marshal(req)

		result, _ := handleAuthParse(reqBody)

		var env abiEnvelope
		json.Unmarshal(result, &env)

		var body map[string]any
		json.Unmarshal(env.Result, &body)

		handled, _ := body["Handled"].(bool)
		if handled {
			t.Error("expected Handled=false for non-joycode type")
		}
	})

	t.Run("missing ptKey returns Handled=false", func(t *testing.T) {
		raw := map[string]any{"type": "joycode"}
		rawJSON, _ := json.Marshal(raw)

		req := authParseRequest{
			RawJSON: base64.StdEncoding.EncodeToString(rawJSON),
		}
		reqBody, _ := json.Marshal(req)

		result, _ := handleAuthParse(reqBody)

		var env abiEnvelope
		json.Unmarshal(result, &env)

		var body map[string]any
		json.Unmarshal(env.Result, &body)

		handled, _ := body["Handled"].(bool)
		if handled {
			t.Error("expected Handled=false when ptKey missing")
		}
	})

	t.Run("invalid base64 returns parse_error", func(t *testing.T) {
		req := authParseRequest{RawJSON: "not-base64!!!"}
		reqBody, _ := json.Marshal(req)

		result, _ := handleAuthParse(reqBody)

		var env abiEnvelope
		json.Unmarshal(result, &env)
		if env.OK {
			t.Error("expected error for invalid base64")
		}
		if env.Error.Code != "parse_error" {
			t.Errorf("error code = %q, want parse_error", env.Error.Code)
		}
	})
}

func TestABIRegistrationSerializesCorrectFieldNames(t *testing.T) {
	raw, err := handleRegister(nil)
	if err != nil {
		t.Fatalf("handleRegister() error = %v", err)
	}

	var env abiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("json.Unmarshal(envelope) error = %v", err)
	}
	if !env.OK {
		t.Fatalf("expected OK, got error: %v", env.Error)
	}

	var result struct {
		SchemaVersion uint32             `json:"schema_version"`
		Metadata      map[string]any     `json:"metadata"`
		Capabilities  map[string]any     `json:"capabilities"`
	}
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("json.Unmarshal(result) error = %v", err)
	}

	if result.SchemaVersion == 0 {
		t.Error("schema_version is 0, expected non-zero")
	}

	for _, key := range []string{"Name", "Version", "Author", "GitHubRepository"} {
		if _, ok := result.Metadata[key]; !ok {
			t.Errorf("metadata missing key %q, got keys: %v", key, mapKeys(result.Metadata))
		}
	}

	name, _ := result.Metadata["Name"].(string)
	if name != "joycode" {
		t.Errorf("metadata.Name = %q, want joycode", name)
	}

	for _, key := range []string{"executor", "auth_provider", "model_provider"} {
		val, ok := result.Capabilities[key]
		if !ok {
			t.Errorf("capabilities missing key %q, got keys: %v", key, mapKeys(result.Capabilities))
			continue
		}
		if val != true {
			t.Errorf("capabilities[%q] = %v, want true", key, val)
		}
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestHandleModelStatic(t *testing.T) {
	result, err := handleModelStatic(nil)
	if err != nil {
		t.Fatalf("handleModelStatic() error = %v", err)
	}

	var env abiEnvelope
	json.Unmarshal(result, &env)
	if !env.OK {
		t.Fatalf("expected OK, got error: %v", env.Error)
	}

	var body map[string]any
	json.Unmarshal(env.Result, &body)

	if body["provider"] != "joycode" {
		t.Errorf("provider = %v, want joycode", body["provider"])
	}
	models, _ := body["models"].([]any)
	if len(models) != len(staticModels) {
		t.Errorf("got %d models, want %d", len(models), len(staticModels))
	}
}
