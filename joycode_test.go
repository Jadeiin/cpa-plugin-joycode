package main

import (
	"bytes"
	"compress/gzip"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
		{"empty string falls back", map[string]any{"loginType": ""}, "PIN_JD_CLOUD"},
		{"missing key falls back", map[string]any{}, "PIN_JD_CLOUD"},
		{"wrong type falls back", map[string]any{"loginType": 123}, "PIN_JD_CLOUD"},
		{"nil metadata", nil, "PIN_JD_CLOUD"},
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
		raw     string
		scheme  string
		host    string
		port    string
		path    string
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

func TestNextSSEEventAllowsLargeCompleteChunks(t *testing.T) {
	event := []byte("data: {}\n\n")
	buffer := bytes.Repeat(event, 120000)
	buffer = append(buffer, []byte("data: pending")...)

	if len(buffer) <= 1024*1024 {
		t.Fatalf("test setup buffer length = %d, want over 1MB", len(buffer))
	}

	events := collectSSEEvents(&buffer)

	if len(events) != 120000 {
		t.Fatalf("collectSSEEvents() returned %d events, want 120000", len(events))
	}
	if string(buffer) != "data: pending" {
		t.Fatalf("remaining buffer = %q, want pending event", string(buffer))
	}
	if len(buffer) > 1024*1024 {
		t.Fatalf("remaining buffer length = %d, want under 1MB", len(buffer))
	}
}

func TestNextSSEEventRecognizesSplitCRLFDelimiter(t *testing.T) {
	buffer := []byte("data: first\r\n\r")

	if events := collectSSEEvents(&buffer); len(events) != 0 {
		t.Fatalf("collectSSEEvents() returned %d events before delimiter completed, want 0", len(events))
	}

	buffer = append(buffer, []byte("\ndata: second\r\n\r\npending")...)
	events := collectSSEEvents(&buffer)

	if len(events) != 2 {
		t.Fatalf("collectSSEEvents() returned %d events, want 2", len(events))
	}
	if string(events[0]) != "data: first" {
		t.Fatalf("first event = %q, want data: first", string(events[0]))
	}
	if string(events[1]) != "data: second" {
		t.Fatalf("second event = %q, want data: second", string(events[1]))
	}
	if string(buffer) != "pending" {
		t.Fatalf("remaining buffer = %q, want pending", string(buffer))
	}
}

func TestSSEEventDataPayloadCombinesMultilineDataFields(t *testing.T) {
	event := []byte("data: {\"choices\":[\ndata: {\"delta\":{\"content\":\"hi\"}}\ndata: ]}")

	got := sseEventDataPayload(event)
	want := []byte("{\"choices\":[\n{\"delta\":{\"content\":\"hi\"}}\n]}")

	if !bytes.Equal(got, want) {
		t.Fatalf("sseEventDataPayload() = %q, want %q", got, want)
	}
}

func collectSSEEvents(buffer *[]byte) [][]byte {
	var events [][]byte
	for {
		event, ok := nextSSEEvent(buffer)
		if !ok {
			return events
		}
		events = append(events, append([]byte(nil), event...))
	}
}

func TestSSEEventDataPayloadSkipsDone(t *testing.T) {
	if got := sseEventDataPayload([]byte("event: message\ndata: [DONE]")); got != nil {
		t.Fatalf("sseEventDataPayload() = %q, want nil", got)
	}
}

func TestInjectPayloadFields(t *testing.T) {
	t.Run("sets required fields", func(t *testing.T) {
		input := map[string]any{"messages": []any{"hello"}}
		inputJSON, _ := json.Marshal(input)

		result := injectPayloadFields(inputJSON, "GLM-5.1")

		var out map[string]any
		if err := json.Unmarshal(result, &out); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}

		if out["model"] != "GLM-5.1" {
			t.Errorf("model = %v, want GLM-5.1", out["model"])
		}
		if _, ok := out["stream_options"]; !ok {
			t.Error("missing stream_options")
		}
	})

	t.Run("reasoning model gets default thinking disabled", func(t *testing.T) {
		input := map[string]any{"messages": []any{"hello"}}
		inputJSON, _ := json.Marshal(input)

		for _, model := range []string{"GLM-5.1", "Kimi-K2.6", "MiniMax-M2.7"} {
			result := injectPayloadFields(inputJSON, model)
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

		result := injectPayloadFields(inputJSON, "GLM-5.1")
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

		result := injectPayloadFields(inputJSON, "JoyAI-Code")
		var out map[string]any
		json.Unmarshal(result, &out)
		thinking, _ := out["thinking"].(map[string]any)
		if thinking["type"] != "disabled" {
			t.Errorf("non-reasoning model thinking = %v, want disabled", thinking)
		}
	})

	t.Run("invalid JSON passthrough", func(t *testing.T) {
		bad := []byte("{not valid json")
		result := injectPayloadFields(bad, "JoyAI-Code")
		if !bytes.Equal(result, bad) {
			t.Error("invalid JSON should be returned unchanged")
		}
	})
}

func TestBuildJCHeaders(t *testing.T) {
	headers := buildJCHeaders("my-ptkey", "PIN_JD_CLOUD", "JD")

	ptKey, _ := headers["ptKey"].([]string)
	if len(ptKey) == 0 || ptKey[0] != "my-ptkey" {
		t.Errorf("ptKey header = %v, want [my-ptkey]", ptKey)
	}

	loginType, _ := headers["loginType"].([]string)
	if len(loginType) == 0 || loginType[0] != "PIN_JD_CLOUD" {
		t.Errorf("loginType header = %v, want [PIN_JD_CLOUD]", loginType)
	}

	tenant, _ := headers["tenant"].([]string)
	if len(tenant) == 0 || tenant[0] != "JD" {
		t.Errorf("tenant header = %v, want [JD]", tenant)
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
			"type":        "joycode",
			"ptKey":       "test-pt-key",
			"userId":      "user1",
			"tenant":      "JD",
			"orgFullName": "Org",
			"loginType":   "PIN_JD_CLOUD",
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
		SchemaVersion uint32         `json:"schema_version"`
		Metadata      map[string]any `json:"metadata"`
		Capabilities  map[string]any `json:"capabilities"`
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

func TestColorGatewaySign(t *testing.T) {
	// Verify HMAC-SHA256 algorithm against captured mitmproxy flow data.
	tests := []struct {
		name     string
		signStr  string
		expected string
	}{
		{
			name:     "modelList",
			signStr:  "joycode_ide&joycode_modelList&1781629681134",
			expected: "469fe1b57c53995da45f01656713a6bc40b1e50cb930e1d47d0b7f2908a8f71c",
		},
		{
			name:     "chat_completions",
			signStr:  "joycode_ide&chat_completions&1781631584594",
			expected: "2956521680734ae9d1316c0751b5b5f0744cc1533782fe04c2cc21fef3e7dae4",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mac := hmac.New(sha256.New, []byte(JCColorSecret))
			mac.Write([]byte(tt.signStr))
			got := hex.EncodeToString(mac.Sum(nil))
			if got != tt.expected {
				t.Errorf("HMAC-SHA256(%q) = %s, want %s", tt.signStr, got, tt.expected)
			}
		})
	}
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
