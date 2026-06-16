package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// JoyCode API constants.
const (
	JCBaseURL       = "https://joycode-api.jd.com"
	JCUserInfoPath  = "/api/saas/user/v1/userInfo"
	JCModelList     = "/api/saas/models/v1/modelList"
	JCChatPath      = "/api/saas/openai/v1/chat/completions"
	JCClientVersion = "2.4.8"
	JCUserAgent     = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) " +
		"JoyCode/2.4.8 Chrome/133.0.0.0 Electron/35.2.0 Safari/537.36"
	defaultLoginType = "N_PIN_PC"
)

// Known JoyCode models.
var staticModels = []modelInfo{
	{ID: "JoyAI-Code", Object: "model", OwnedBy: "joycode", DisplayName: "JoyAI Code"},
	{ID: "GLM-5.1", Object: "model", OwnedBy: "joycode", DisplayName: "GLM 5.1"},
	{ID: "Kimi-K2.6", Object: "model", OwnedBy: "joycode", DisplayName: "Kimi K2.6"},
	{ID: "MiniMax-M2.7", Object: "model", OwnedBy: "joycode", DisplayName: "MiniMax M2.7"},
	{ID: "Doubao-Seed-2.0-pro", Object: "model", OwnedBy: "joycode", DisplayName: "Doubao Seed 2.0 Pro"},
}

type modelInfo struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	OwnedBy     string `json:"owned_by"`
	DisplayName string `json:"display_name,omitempty"`
}

// --- executor.identifier ---

func handleExecutorIdentifier() ([]byte, error) {
	return abiOKEnvelope(map[string]string{"identifier": "joycode"})
}

// --- executor.execute ---

type executorRequest struct {
	AuthID          string                 `json:"AuthID"`
	AuthProvider    string                 `json:"AuthProvider"`
	Model           string                 `json:"Model"`
	Format          string                 `json:"Format"`
	Stream          bool                   `json:"Stream"`
	Alt             string                 `json:"Alt"`
	Headers         map[string][]string    `json:"Headers"`
	Query           map[string][]string    `json:"Query"`
	OriginalRequest string                 `json:"OriginalRequest"`
	SourceFormat    string                 `json:"SourceFormat"`
	Payload         string                 `json:"Payload"`
	Metadata        map[string]any         `json:"Metadata"`
	StorageJSON     string                 `json:"StorageJSON"`
	AuthMetadata    map[string]any         `json:"AuthMetadata"`
	AuthAttributes  map[string]string      `json:"AuthAttributes"`
	StreamID        string                 `json:"stream_id"`
	HostCallbackID  string                 `json:"host_callback_id"`
}

type executorResponse struct {
	Payload  string              `json:"Payload"`
	Headers  map[string][]string `json:"Headers"`
	Metadata map[string]any      `json:"Metadata,omitempty"`
}

func loginTypeFromAuthMetadata(meta map[string]any) string {
	if lt, ok := meta["loginType"].(string); ok && lt != "" {
		return lt
	}
	return defaultLoginType
}

func handleExecutorExecute(reqBody []byte) ([]byte, error) {
	var req executorRequest
	if err := json.Unmarshal(reqBody, &req); err != nil {
		return nil, fmt.Errorf("unmarshal executor request: %w", err)
	}

	payloadBytes, err := base64.StdEncoding.DecodeString(req.Payload)
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}

	ptKey, _ := req.AuthMetadata["ptKey"].(string)
	if ptKey == "" {
		return nil, fmt.Errorf("joycode: missing ptKey in auth metadata")
	}
	loginType := loginTypeFromAuthMetadata(req.AuthMetadata)
	userID, _ := req.AuthMetadata["userId"].(string)

	modifiedPayload := injectPayloadFields(payloadBytes, req.Model, userID)
	headers := buildJCHeaders(ptKey, loginType)

	httpReq := map[string]any{
		"method":  "POST",
		"url":     JCBaseURL + JCChatPath,
		"headers": headers,
		"body":    base64.StdEncoding.EncodeToString(modifiedPayload),
	}
	if req.HostCallbackID != "" {
		httpReq["host_callback_id"] = req.HostCallbackID
	}

	respJSON, err := callHostJSON("host.http.do", httpReq)
	if err != nil {
		return nil, fmt.Errorf("joycode http call: %w", err)
	}

	var httpResp struct {
		StatusCode int                 `json:"status_code"`
		Headers    map[string][]string `json:"headers"`
		Body       string              `json:"body"`
	}
	if err := json.Unmarshal(respJSON, &httpResp); err != nil {
		return nil, fmt.Errorf("unmarshal http response: %w", err)
	}

	if httpResp.StatusCode != 200 {
		bodyBytes, _ := base64.StdEncoding.DecodeString(httpResp.Body)
		return nil, fmt.Errorf("joycode: API returned %d: %s", httpResp.StatusCode, string(bodyBytes))
	}

	respBody, err := base64.StdEncoding.DecodeString(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("decode response body: %w", err)
	}

	respBody = decompressGzip(respBody, httpResp.Headers)

	return abiOKEnvelope(executorResponse{
		Payload: base64.StdEncoding.EncodeToString(respBody),
		Headers: httpResp.Headers,
	})
}

// --- executor.execute_stream ---

func handleExecutorExecuteStream(reqBody []byte) ([]byte, error) {
	var req executorRequest
	if err := json.Unmarshal(reqBody, &req); err != nil {
		return nil, fmt.Errorf("unmarshal executor stream request: %w", err)
	}

	if req.StreamID == "" {
		return nil, fmt.Errorf("joycode: stream_id is required for streaming")
	}

	payloadBytes, err := base64.StdEncoding.DecodeString(req.Payload)
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}

	ptKey, _ := req.AuthMetadata["ptKey"].(string)
	if ptKey == "" {
		emitStreamError(req.StreamID, "joycode: missing ptKey in auth metadata")
		return nil, fmt.Errorf("missing ptKey")
	}
	loginType := loginTypeFromAuthMetadata(req.AuthMetadata)
	userID, _ := req.AuthMetadata["userId"].(string)

	modifiedPayload := injectPayloadFields(payloadBytes, req.Model, userID)
	headers := buildJCHeaders(ptKey, loginType)

	streamReq := map[string]any{
		"method":  "POST",
		"url":     JCBaseURL + JCChatPath,
		"headers": headers,
		"body":    base64.StdEncoding.EncodeToString(modifiedPayload),
	}
	if req.HostCallbackID != "" {
		streamReq["host_callback_id"] = req.HostCallbackID
	}

	respJSON, err := callHostJSON("host.http.do_stream", streamReq)
	if err != nil {
		emitStreamError(req.StreamID, fmt.Sprintf("joycode stream connect: %v", err))
		return nil, fmt.Errorf("joycode stream: %w", err)
	}

	var streamResp struct {
		StatusCode int                 `json:"status_code"`
		Headers    map[string][]string `json:"headers"`
		StreamID   string              `json:"stream_id"`
	}
	if err := json.Unmarshal(respJSON, &streamResp); err != nil {
		emitStreamError(req.StreamID, fmt.Sprintf("unmarshal stream response: %v", err))
		return nil, fmt.Errorf("unmarshal stream response: %w", err)
	}

	if streamResp.StatusCode != 200 {
		errBody := readStreamAll(streamResp.StreamID)
		emitStreamError(req.StreamID, fmt.Sprintf("joycode: API returned %d: %s", streamResp.StatusCode, string(errBody)))
		closeStream(streamResp.StreamID, "")
		return nil, fmt.Errorf("joycode: API returned %d", streamResp.StatusCode)
	}

	initialResp := map[string]any{
		"Headers": streamResp.Headers,
		"Chunks":  []any{},
	}
	result, err := abiOKEnvelope(initialResp)
	if err != nil {
		return nil, err
	}

	go readAndEmitStreamChunks(streamResp.StreamID, req.StreamID)

	return result, nil
}

func readAndEmitStreamChunks(httpStreamID, pluginStreamID string) {
	defer closeStream(pluginStreamID, "")
	defer closeHTTPStream(httpStreamID)

	scannerBuf := make([]byte, 0, 1024*1024)

	for {
		chunkJSON, err := callHostJSON("host.http.stream_read", map[string]any{"stream_id": httpStreamID})
		if err != nil {
			emitStreamError(pluginStreamID, fmt.Sprintf("stream read error: %v", err))
			return
		}

		var readResp struct {
			Payload string `json:"payload"`
			Error   string `json:"error"`
			Done    bool   `json:"done"`
		}
		if err := json.Unmarshal(chunkJSON, &readResp); err != nil {
			emitStreamError(pluginStreamID, fmt.Sprintf("unmarshal stream read: %v", err))
			return
		}

		if readResp.Error != "" {
			emitStreamError(pluginStreamID, readResp.Error)
			return
		}
		if readResp.Done {
			return
		}

		chunkBytes, err := base64.StdEncoding.DecodeString(readResp.Payload)
		if err != nil {
			continue
		}

		scannerBuf = append(scannerBuf[:0], chunkBytes...)
		lines := splitLines(scannerBuf)

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			var data string
			if strings.HasPrefix(line, "data: ") {
				data = strings.TrimPrefix(line, "data: ")
			} else if strings.HasPrefix(line, "data:") {
				data = strings.TrimPrefix(line, "data:")
			} else {
				emitStreamChunk(pluginStreamID, []byte(line+"\n"))
				continue
			}

			if data == "[DONE]" {
				emitStreamChunk(pluginStreamID, []byte("data: [DONE]\n\n"))
				return
			}

			emitStreamChunk(pluginStreamID, []byte("data: "+data+"\n\n"))
		}
	}
}

func emitStreamChunk(streamID string, payload []byte) {
	req := map[string]any{
		"stream_id": streamID,
		"payload":   base64.StdEncoding.EncodeToString(payload),
		"error":     "",
	}
	p, _ := json.Marshal(req)
	callHost("host.stream.emit", p)
}

func emitStreamError(streamID string, errMsg string) {
	req := map[string]any{
		"stream_id": streamID,
		"payload":   "",
		"error":     errMsg,
	}
	p, _ := json.Marshal(req)
	callHost("host.stream.emit", p)
}

func closeStream(streamID string, errStr string) {
	req := map[string]any{
		"stream_id": streamID,
		"error":     errStr,
	}
	p, _ := json.Marshal(req)
	callHost("host.stream.close", p)
}

func closeHTTPStream(streamID string) {
	p, _ := json.Marshal(map[string]any{"stream_id": streamID})
	callHost("host.http.stream_close", p)
}

func readStreamAll(streamID string) []byte {
	var buf []byte
	for {
		chunkJSON, err := callHostJSON("host.http.stream_read", map[string]any{"stream_id": streamID})
		if err != nil {
			break
		}
		var readResp struct {
			Payload string `json:"payload"`
			Done    bool   `json:"done"`
			Error   string `json:"error"`
		}
		if json.Unmarshal(chunkJSON, &readResp) != nil || readResp.Done || readResp.Error != "" {
			break
		}
		b, _ := base64.StdEncoding.DecodeString(readResp.Payload)
		buf = append(buf, b...)
	}
	closeHTTPStream(streamID)
	return buf
}

// --- executor.count_tokens ---

func handleCountTokens() ([]byte, error) {
	return nil, fmt.Errorf("joycode: token counting not supported")
}

// --- auth.identifier ---

func handleAuthIdentifier() ([]byte, error) {
	return abiOKEnvelope(map[string]string{"identifier": "joycode"})
}

// --- auth.parse ---

type authParseRequest struct {
	Provider string         `json:"Provider"`
	Path     string         `json:"Path"`
	FileName string         `json:"FileName"`
	RawJSON  string         `json:"RawJSON"`
	Host     map[string]any `json:"Host"`
}

type authData struct {
	Provider         string            `json:"Provider"`
	ID               string            `json:"ID"`
	FileName         string            `json:"FileName"`
	Label            string            `json:"Label"`
	Prefix           string            `json:"Prefix,omitempty"`
	ProxyURL         string            `json:"ProxyURL,omitempty"`
	Disabled         bool              `json:"Disabled,omitempty"`
	StorageJSON      string            `json:"StorageJSON"`
	Metadata         map[string]any    `json:"Metadata"`
	Attributes       map[string]string `json:"Attributes,omitempty"`
	NextRefreshAfter string            `json:"NextRefreshAfter,omitempty"`
}

func handleAuthParse(reqBody []byte) ([]byte, error) {
	var req authParseRequest
	if err := json.Unmarshal(reqBody, &req); err != nil {
		return nil, fmt.Errorf("unmarshal auth parse request: %w", err)
	}

	rawBytes, err := base64.StdEncoding.DecodeString(req.RawJSON)
	if err != nil {
		return abiErrorEnvelope("parse_error", "failed to decode RawJSON"), nil
	}

	var data map[string]any
	if err := json.Unmarshal(rawBytes, &data); err != nil {
		return abiErrorEnvelope("parse_error", "invalid JSON"), nil
	}

	authType, _ := data["type"].(string)
	if authType != "joycode" {
		return abiOKEnvelope(map[string]any{"Handled": false})
	}

	ptKey, _ := data["ptKey"].(string)
	userID, _ := data["userId"].(string)
	tenant, _ := data["tenant"].(string)
	orgFullName, _ := data["orgFullName"].(string)
	loginType, _ := data["loginType"].(string)

	if ptKey == "" {
		return abiOKEnvelope(map[string]any{"Handled": false})
	}

	label := userID
	if label == "" {
		label = "joycode"
	}
	fileName := req.FileName
	if fileName == "" {
		if userID != "" {
			fileName = fmt.Sprintf("joycode-%s.json", userID)
		} else {
			fileName = "joycode-token.json"
		}
	}

	storageJSON := base64.StdEncoding.EncodeToString(rawBytes)

	return abiOKEnvelope(map[string]any{
		"Handled": true,
		"Auth": authData{
			Provider:    "joycode",
			ID:          fileName,
			FileName:    fileName,
			Label:       label,
			StorageJSON: storageJSON,
			Metadata: map[string]any{
				"type":        "joycode",
				"ptKey":       ptKey,
				"userId":      userID,
				"tenant":      tenant,
				"orgFullName": orgFullName,
				"loginType":   loginType,
			},
			Attributes: map[string]string{
				"provider": "joycode",
				"source":   "file",
			},
		},
	})
}

// --- auth.login.start ---

type authLoginStartRequest struct {
	Provider       string         `json:"Provider"`
	BaseURL        string         `json:"BaseURL,omitempty"`
	Host           map[string]any `json:"Host"`
	Metadata       map[string]any `json:"Metadata,omitempty"`
	HostCallbackID string         `json:"host_callback_id"`
}

func handleAuthLoginStart(reqBody []byte) ([]byte, error) {
	var req authLoginStartRequest
	if err := json.Unmarshal(reqBody, &req); err != nil {
		return nil, fmt.Errorf("unmarshal auth login start: %w", err)
	}

	port := extractPortFromURL(req.BaseURL)
	if port == 0 {
		port = 8317
	}

	authKeyBytes := make([]byte, 16)
	rand.Read(authKeyBytes)
	authKey := hex.EncodeToString(authKeyBytes)

	loginURL := fmt.Sprintf(
		"https://joycode.jd.com/login/?ideAppName=JoyCode&fromIde=ide&redirect=0&authPort=%d&authKey=%s",
		port, authKey,
	)

	stateBytes := make([]byte, 16)
	rand.Read(stateBytes)
	stateID := hex.EncodeToString(stateBytes)

	return abiOKEnvelope(map[string]any{
		"Provider":  "joycode",
		"URL":       loginURL,
		"State":     stateID,
		"ExpiresAt": time.Now().Add(5 * time.Minute).Format(time.RFC3339),
		"Metadata": map[string]any{
			"auth_key":   authKey,
			"login_type": "browser_oauth",
			"port":       port,
		},
	})
}

func extractPortFromURL(rawURL string) int {
	u, err := parseSimpleURL(rawURL)
	if err == nil && u.Port != "" {
		p, _ := simpleAtoi(u.Port)
		return p
	}
	if idx := strings.Index(rawURL, "127.0.0.1:"); idx >= 0 {
		rest := rawURL[idx+len("127.0.0.1:"):]
		end := 0
		for end < len(rest) && (rest[end] >= '0' && rest[end] <= '9') {
			end++
		}
		p, _ := simpleAtoi(rest[:end])
		return p
	}
	if idx := strings.LastIndex(rawURL, ":"); idx >= 0 {
		rest := rawURL[idx+1:]
		end := 0
		for end < len(rest) && (rest[end] >= '0' && rest[end] <= '9') {
			end++
		}
		if end > 0 {
			p, _ := simpleAtoi(rest[:end])
			return p
		}
	}
	return 0
}

// --- auth.login.poll ---

type authLoginPollRequest struct {
	Provider       string         `json:"Provider"`
	State          string         `json:"State"`
	Host           map[string]any `json:"Host"`
	Metadata       map[string]any `json:"Metadata,omitempty"`
	HostCallbackID string         `json:"host_callback_id"`
}

func handleAuthLoginPoll(reqBody []byte) ([]byte, error) {
	var req authLoginPollRequest
	if err := json.Unmarshal(reqBody, &req); err != nil {
		return nil, fmt.Errorf("unmarshal auth login poll: %w", err)
	}

	authListResult, err := callHostJSON("host.auth.list", map[string]any{})
	if err != nil {
		hostLog("debug", fmt.Sprintf("joycode poll: auth list check failed: %v", err))
	}

	if err == nil && authListResult != nil {
		var authList struct {
			Files []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Provider string `json:"provider"`
				Label    string `json:"label"`
				Name     string `json:"name"`
			} `json:"files"`
		}
		if json.Unmarshal(authListResult, &authList) == nil {
			for _, f := range authList.Files {
				if f.Provider == "joycode" || f.Type == "joycode" {
					authGetResult, errGet := callHostJSON("host.auth.get", map[string]any{
						"auth_index": f.ID,
					})
					if errGet != nil {
						continue
					}
					var authGet struct {
						JSON map[string]any `json:"json"`
					}
					if json.Unmarshal(authGetResult, &authGet) == nil {
						ptKey, _ := authGet.JSON["ptKey"].(string)
						if ptKey != "" {
							loginType, _ := authGet.JSON["loginType"].(string)
							if loginType == "" {
								loginType = defaultLoginType
							}
							tokenData, errVerify := verifyJoyCodeToken(ptKey, loginType)
							if errVerify != nil {
								userID, _ := authGet.JSON["userId"].(string)
								tenant, _ := authGet.JSON["tenant"].(string)
								orgFullName, _ := authGet.JSON["orgFullName"].(string)
								if tenant == "" {
									tenant = "JD"
								}

								label := userID
								if label == "" {
									label = "joycode"
								}
								fileName := f.Name
								if fileName == "" {
									fileName = "joycode-token.json"
								}

								storageBytes, _ := json.Marshal(authGet.JSON)
								return abiOKEnvelope(map[string]any{
									"Status":  "success",
									"Message": fmt.Sprintf("JoyCode credentials detected: %s", label),
									"Auth": authData{
										Provider:    "joycode",
										ID:          fileName,
										FileName:    fileName,
										Label:       label,
										StorageJSON: base64.StdEncoding.EncodeToString(storageBytes),
										Metadata: map[string]any{
											"type":        "joycode",
											"ptKey":       ptKey,
											"userId":      userID,
											"tenant":      tenant,
											"orgFullName": orgFullName,
											"loginType":   loginType,
										},
										Attributes: map[string]string{
											"provider": "joycode",
											"source":   "oauth_poll",
										},
									},
								})
							}

							tenant := tokenData.Tenant
							if tenant == "" {
								tenant = "JD"
							}
							userID := tokenData.UserID
							orgFullName := tokenData.OrgFullName

							label := userID
							if label == "" {
								label = "joycode"
							}
							fileName := f.Name
							if fileName == "" {
								fileName = fmt.Sprintf("joycode-%s.json", userID)
							}

							storage := map[string]any{
								"type":         "joycode",
								"ptKey":        tokenData.PtKey,
								"userId":       userID,
								"tenant":       tenant,
								"orgFullName":  orgFullName,
								"loginType":    tokenData.LoginType,
								"last_refresh": time.Now().Format(time.RFC3339),
							}
							storageBytes, _ := json.Marshal(storage)

							return abiOKEnvelope(map[string]any{
								"Status":  "success",
								"Message": fmt.Sprintf("Login successful! User: %s", label),
								"Auth": authData{
									Provider:    "joycode",
									ID:          fileName,
									FileName:    fileName,
									Label:       label,
									StorageJSON: base64.StdEncoding.EncodeToString(storageBytes),
									Metadata: map[string]any{
										"type":        "joycode",
										"ptKey":       tokenData.PtKey,
										"userId":      userID,
										"tenant":      tenant,
										"orgFullName": orgFullName,
										"loginType":   tokenData.LoginType,
									},
									Attributes: map[string]string{
										"provider": "joycode",
										"source":   "oauth_poll",
									},
								},
							})
						}
					}
				}
			}
		}
	}

	return abiOKEnvelope(map[string]any{
		"Status":  "pending",
		"Message": "Open the JoyCode login URL in your browser. After login, copy pt_key from the redirect URL and create a joycode JSON auth file manually.",
	})
}

// --- auth.refresh ---

type authRefreshRequest struct {
	AuthID         string            `json:"AuthID"`
	AuthProvider   string            `json:"AuthProvider"`
	StorageJSON    string            `json:"StorageJSON"`
	Metadata       map[string]any    `json:"Metadata"`
	Attributes     map[string]string `json:"Attributes"`
	Host           map[string]any    `json:"Host,omitempty"`
	HostCallbackID string            `json:"host_callback_id"`
}

func handleAuthRefresh(reqBody []byte) ([]byte, error) {
	var req authRefreshRequest
	if err := json.Unmarshal(reqBody, &req); err != nil {
		return nil, fmt.Errorf("unmarshal auth refresh request: %w", err)
	}

	ptKey, _ := req.Metadata["ptKey"].(string)
	if ptKey == "" {
		return nil, fmt.Errorf("joycode: missing ptKey for refresh")
	}

	loginType := loginTypeFromAuthMetadata(req.Metadata)

	tokenData, err := verifyJoyCodeToken(ptKey, loginType)
	if err != nil {
		return abiOKEnvelope(map[string]any{
			"Auth": authData{
				Provider:    "joycode",
				ID:          req.AuthID,
				FileName:    req.AuthID,
				StorageJSON: req.StorageJSON,
				Metadata:    req.Metadata,
				Attributes:  req.Attributes,
				Disabled:    true,
			},
			"NextRefreshAfter": time.Now().Add(1 * time.Hour).Format(time.RFC3339),
		})
	}

	tenant := tokenData.Tenant
	if tenant == "" {
		tenant = "JD"
	}

	storage := map[string]any{
		"type":         "joycode",
		"ptKey":        tokenData.PtKey,
		"userId":       tokenData.UserID,
		"tenant":       tenant,
		"orgFullName":  tokenData.OrgFullName,
		"loginType":    tokenData.LoginType,
		"last_refresh": time.Now().Format(time.RFC3339),
	}
	storageBytes, _ := json.Marshal(storage)

	label := tokenData.UserID
	if label == "" {
		label = "joycode"
	}
	fileName := "joycode-token.json"
	if tokenData.UserID != "" {
		fileName = fmt.Sprintf("joycode-%s.json", tokenData.UserID)
	}

	return abiOKEnvelope(map[string]any{
		"Auth": authData{
			Provider:    "joycode",
			ID:          fileName,
			FileName:    fileName,
			Label:       label,
			StorageJSON: base64.StdEncoding.EncodeToString(storageBytes),
			Metadata: map[string]any{
				"type":        "joycode",
				"ptKey":       tokenData.PtKey,
				"userId":      tokenData.UserID,
				"tenant":      tenant,
				"orgFullName": tokenData.OrgFullName,
				"loginType":   tokenData.LoginType,
			},
			Attributes: req.Attributes,
		},
		"NextRefreshAfter": time.Now().Add(24 * time.Hour).Format(time.RFC3339),
	})
}

// --- model.static ---

type modelHostInfo struct {
	AuthDir          string         `json:"AuthDir"`
	ProxyURL         string         `json:"ProxyURL,omitempty"`
	ForceModelPrefix bool           `json:"ForceModelPrefix,omitempty"`
	OAuthModelAlias  map[string]any `json:"OAuthModelAlias,omitempty"`
	ExcludedModels   map[string]any `json:"ExcludedModels,omitempty"`
}

type modelStaticRequest struct {
	Plugin map[string]any `json:"Plugin"`
	Host   modelHostInfo  `json:"Host"`
}

func handleModelStatic(reqBody []byte) ([]byte, error) {
	now := time.Now().Unix()
	models := make([]map[string]any, len(staticModels))
	for i, m := range staticModels {
		models[i] = map[string]any{
			"id":           m.ID,
			"object":       m.Object,
			"created":      now,
			"owned_by":     m.OwnedBy,
			"display_name": m.DisplayName,
		}
	}
	return abiOKEnvelope(map[string]any{
		"provider": "joycode",
		"models":   models,
	})
}

// --- model.for_auth ---

type modelForAuthRequest struct {
	Plugin         map[string]any    `json:"Plugin"`
	AuthID         string            `json:"auth_id"`
	AuthProvider   string            `json:"auth_provider"`
	StorageJSON    string            `json:"storage_json"`
	Metadata       map[string]any    `json:"metadata"`
	Attributes     map[string]string `json:"attributes,omitempty"`
	Host           modelHostInfo     `json:"Host"`
	HostCallbackID string            `json:"host_callback_id"`
}

func handleModelForAuth(reqBody []byte) ([]byte, error) {
	var req modelForAuthRequest
	if err := json.Unmarshal(reqBody, &req); err != nil {
		return nil, fmt.Errorf("unmarshal model for_auth request: %w", err)
	}

	ptKey, _ := req.Metadata["ptKey"].(string)
	if ptKey == "" {
		return handleModelStatic(reqBody)
	}

	loginType := loginTypeFromAuthMetadata(req.Metadata)

	headers := map[string]any{
		"Content-Type": []string{"application/json; charset=UTF-8"},
		"ptKey":        []string{ptKey},
		"loginType":    []string{loginType},
		"User-Agent":   []string{JCUserAgent},
		"Accept":       []string{"application/json"},
	}

	emptyBody := base64.StdEncoding.EncodeToString([]byte("{}"))
	httpReq := map[string]any{
		"method":  "POST",
		"url":     JCBaseURL + JCModelList,
		"headers": headers,
		"body":    emptyBody,
	}
	if req.HostCallbackID != "" {
		httpReq["host_callback_id"] = req.HostCallbackID
	}

	respJSON, err := callHostJSON("host.http.do", httpReq)
	if err != nil {
		hostLog("warn", fmt.Sprintf("joycode: model list fetch failed: %v, using static models", err))
		return handleModelStatic(reqBody)
	}

	var httpResp struct {
		StatusCode int                 `json:"status_code"`
		Headers    map[string][]string `json:"headers"`
		Body       string              `json:"body"`
	}
	if err := json.Unmarshal(respJSON, &httpResp); err != nil {
		hostLog("warn", fmt.Sprintf("joycode: model list response parse failed: %v, using static models", err))
		return handleModelStatic(reqBody)
	}

	if httpResp.StatusCode != 200 {
		hostLog("warn", fmt.Sprintf("joycode: model list API returned %d, using static models", httpResp.StatusCode))
		return handleModelStatic(reqBody)
	}

	bodyBytes, _ := base64.StdEncoding.DecodeString(httpResp.Body)
	bodyBytes = decompressGzip(bodyBytes, httpResp.Headers)

	var result map[string]any
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		hostLog("warn", fmt.Sprintf("joycode: model list JSON parse failed: %v, using static models", err))
		return handleModelStatic(reqBody)
	}

	code, _ := result["code"].(float64)
	if int(code) != 0 {
		hostLog("warn", fmt.Sprintf("joycode: model list returned code=%v, using static models", code))
		return handleModelStatic(reqBody)
	}

	dataArr, _ := result["data"].([]any)
	if len(dataArr) == 0 {
		hostLog("warn", "joycode: model list returned no data, using static models")
		return handleModelStatic(reqBody)
	}

	now := time.Now().Unix()
	var models []map[string]any
	for _, item := range dataArr {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		hidden, _ := itemMap["hidden"].(bool)
		if hidden {
			continue
		}
		modelID, _ := itemMap["chatApiModel"].(string)
		if modelID == "" {
			modelID, _ = itemMap["label"].(string)
		}
		if modelID == "" {
			continue
		}
		displayName, _ := itemMap["label"].(string)
		models = append(models, map[string]any{
			"id":           modelID,
			"object":       "model",
			"created":      now,
			"owned_by":     "joycode",
			"display_name": displayName,
		})
	}

	if len(models) == 0 {
		return handleModelStatic(reqBody)
	}

	return abiOKEnvelope(map[string]any{
		"provider": "joycode",
		"models":   models,
	})
}

// --- Internal helpers ---

func buildJCHeaders(ptKey, loginType string) map[string]any {
	return map[string]any{
		"Content-Type":    []string{"application/json; charset=UTF-8"},
		"ptKey":           []string{ptKey},
		"loginType":       []string{loginType},
		"User-Agent":      []string{JCUserAgent},
		"Accept":          []string{"*/*"},
		"Accept-Encoding": []string{"gzip, deflate, br"},
		"Accept-Language": []string{"zh-CN,zh;q=0.9,en;q=0.8"},
		"Connection":      []string{"keep-alive"},
	}
}

var reasoningModels = map[string]bool{
	"GLM-5.1":      true,
	"Kimi-K2.6":    true,
	"MiniMax-M2.7": true,
}

func injectPayloadFields(openaiPayload []byte, modelName, userID string) []byte {
	var payload map[string]any
	if err := json.Unmarshal(openaiPayload, &payload); err != nil {
		return openaiPayload
	}

	payload["model"] = modelName
	payload["tenant"] = "JOYCODE"
	payload["userId"] = userID
	payload["client"] = "JoyCode"
	payload["clientVersion"] = JCClientVersion
	payload["stream_options"] = map[string]any{"include_usage": true}
	payload["language"] = "text"
	payload["scene"] = "chat"
	payload["source"] = "joyCoderFe"

	if reasoningModels[modelName] {
		if _, ok := payload["thinking"]; !ok {
			payload["thinking"] = map[string]any{"type": "disabled"}
		}
	} else {
		payload["thinking"] = map[string]any{"type": "disabled"}
	}

	if _, ok := payload["sessionId"]; !ok {
		payload["sessionId"] = newHexID()
	}
	if _, ok := payload["chatId"]; !ok {
		payload["chatId"] = newHexID()
	}
	if _, ok := payload["requestId"]; !ok {
		payload["requestId"] = newHexID()
	}

	result, err := json.Marshal(payload)
	if err != nil {
		return openaiPayload
	}
	return result
}

func newHexID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func decompressGzip(data []byte, headers map[string][]string) []byte {
	isGzip := false
	for _, v := range headers["Content-Encoding"] {
		if strings.Contains(strings.ToLower(v), "gzip") {
			isGzip = true
			break
		}
	}
	if !isGzip {
		return data
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return data
	}
	defer gz.Close()
	decompressed, err := io.ReadAll(gz)
	if err != nil {
		return data
	}
	return decompressed
}

func splitLines(data []byte) []string {
	var lines []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

type joyCodeTokenResult struct {
	PtKey       string
	UserID      string
	Tenant      string
	OrgFullName string
	LoginType   string
}

func verifyJoyCodeToken(ptKey, loginType string) (*joyCodeTokenResult, error) {
	headers := map[string]any{
		"Content-Type": []string{"application/json"},
		"Accept":       []string{"application/json"},
		"ptKey":        []string{ptKey},
		"loginType":    []string{loginType},
		"User-Agent":   []string{JCUserAgent},
	}

	emptyBody := base64.StdEncoding.EncodeToString([]byte("{}"))
	httpReq := map[string]any{
		"method":  "POST",
		"url":     JCBaseURL + JCUserInfoPath,
		"headers": headers,
		"body":    emptyBody,
	}

	respJSON, err := callHostJSON("host.http.do", httpReq)
	if err != nil {
		return nil, fmt.Errorf("userInfo request failed: %w", err)
	}

	var httpResp struct {
		StatusCode int                 `json:"status_code"`
		Headers    map[string][]string `json:"headers"`
		Body       string              `json:"body"`
	}
	if err := json.Unmarshal(respJSON, &httpResp); err != nil {
		return nil, fmt.Errorf("unmarshal userInfo response: %w", err)
	}

	if httpResp.StatusCode != 200 {
		return nil, fmt.Errorf("userInfo returned HTTP %d", httpResp.StatusCode)
	}

	bodyBytes, _ := base64.StdEncoding.DecodeString(httpResp.Body)
	bodyBytes = decompressGzip(bodyBytes, httpResp.Headers)

	var result map[string]any
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, fmt.Errorf("userInfo JSON parse failed: %w", err)
	}

	code, _ := result["code"].(float64)
	if int(code) != 0 {
		msg, _ := result["msg"].(string)
		return nil, fmt.Errorf("userInfo returned code=%v msg=%s", code, msg)
	}

	data, _ := result["data"].(map[string]any)
	if data == nil {
		return nil, fmt.Errorf("userInfo returned nil data")
	}

	userID, _ := data["userId"].(string)
	tenant, _ := data["tenant"].(string)
	if tenant == "" {
		tenant = "JD"
	}
	orgFullName, _ := data["orgFullName"].(string)
	returnedPTKey, _ := data["ptKey"].(string)
	if returnedPTKey == "" {
		returnedPTKey = ptKey
	}

	return &joyCodeTokenResult{
		PtKey:       returnedPTKey,
		UserID:      userID,
		Tenant:      tenant,
		OrgFullName: orgFullName,
		LoginType:   loginType,
	}, nil
}

// --- URL parsing helpers (no net/url import needed for CGo shared library) ---

type simpleURL struct {
	Scheme string
	Host   string
	Port   string
	Path   string
}

func parseSimpleURL(raw string) (*simpleURL, error) {
	schemeEnd := strings.Index(raw, "://")
	if schemeEnd < 0 {
		return nil, fmt.Errorf("no scheme")
	}
	scheme := raw[:schemeEnd]
	rest := raw[schemeEnd+3:]

	slashIdx := strings.Index(rest, "/")
	var hostPart, pathPart string
	if slashIdx >= 0 {
		hostPart = rest[:slashIdx]
		pathPart = rest[slashIdx:]
	} else {
		hostPart = rest
		pathPart = ""
	}

	var host, port string
	colonIdx := strings.LastIndex(hostPart, ":")
	if colonIdx >= 0 {
		host = hostPart[:colonIdx]
		port = hostPart[colonIdx+1:]
	} else {
		host = hostPart
		port = ""
	}

	return &simpleURL{Scheme: scheme, Host: host, Port: port, Path: pathPart}, nil
}

func simpleAtoi(s string) (int, bool) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}
