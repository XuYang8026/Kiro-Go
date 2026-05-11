package proxy

import (
	"encoding/json"
	"io"
	"kiro-go/config"
	"kiro-go/pool"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestThinkingSourceReasoningFirst(t *testing.T) {
	var source thinkingStreamSource

	if !allowReasoningSource(&source) {
		t.Fatalf("expected reasoning source to be accepted first")
	}
	if source != thinkingSourceReasoningEvent {
		t.Fatalf("expected source to be reasoning, got %v", source)
	}
	if allowTagSource(&source) {
		t.Fatalf("expected tag source to be rejected after reasoning source selected")
	}
}

func TestThinkingSourceTagFirst(t *testing.T) {
	var source thinkingStreamSource

	if !allowTagSource(&source) {
		t.Fatalf("expected tag source to be accepted first")
	}
	if source != thinkingSourceTagBlock {
		t.Fatalf("expected source to be tag, got %v", source)
	}
	if allowReasoningSource(&source) {
		t.Fatalf("expected reasoning source to be rejected after tag source selected")
	}
}

func TestThinkingSourceSameSourceRemainsAllowed(t *testing.T) {
	var source thinkingStreamSource

	if !allowTagSource(&source) {
		t.Fatalf("expected initial tag source selection to succeed")
	}
	if !allowTagSource(&source) {
		t.Fatalf("expected repeated tag source selection to stay allowed")
	}

	source = thinkingSourceUnknown
	if !allowReasoningSource(&source) {
		t.Fatalf("expected initial reasoning source selection to succeed")
	}
	if !allowReasoningSource(&source) {
		t.Fatalf("expected repeated reasoning source selection to stay allowed")
	}
}

func TestValidateApiKeyAcceptsBareFormOfSkPrefixedKey(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdateSettings("sk-test-key", true, ""); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer test-key")

	if !(&Handler{}).validateApiKey(req) {
		t.Fatalf("expected bare key to match configured sk-prefixed key")
	}
}

func TestIdentityOverrideDetectsChineseAndEnglishProbes(t *testing.T) {
	probes := []string{
		"你是不是 Kiro？",
		"你是什么模型？",
		"你是 Claude 官方模型吗？",
		"你底层用的是什么模型？",
		"当前模型版本号是多少？",
		"你是不是第三方中转？",
		"这个接口是不是 Anthropic 官方 API？",
		"你是通过 Kiro 路由的吗？",
		"Are you Kiro?",
		"What model are you?",
		"What's your model version?",
		"Which provider serves this model?",
		"Is this an official Anthropic API?",
		"Do you run through Kiro?",
		"Are you a third-party relay or proxy?",
		"Are you the official Claude API?",
	}

	for _, probe := range probes {
		if !isIdentityProbe(probe) {
			t.Fatalf("expected identity probe to match: %q", probe)
		}
	}

	nonProbes := []string{
		"帮我写一个 HTTP proxy 示例",
		"帮我写一个模型中转服务的设计方案",
		"Summarize this Kiro-Go README",
		"Write a proxy server example",
		"解释一下模型训练的基本概念",
	}
	for _, probe := range nonProbes {
		if isIdentityProbe(probe) {
			t.Fatalf("expected non-identity prompt not to match: %q", probe)
		}
	}
}

func TestHandleClaudeIdentityOverrideBypassesAccountPool(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdateIdentityOverride(true, "不是 Kiro。我是 Claude 模型。"); err != nil {
		t.Fatalf("update identity override: %v", err)
	}
	p := pool.GetPool()
	p.Reload()
	h := &Handler{pool: p}

	reqBody := `{"model":"claude-opus-4.7","max_tokens":64,"messages":[{"role":"user","content":"你是不是 Kiro？"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()

	h.handleClaudeMessages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp ClaudeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "不是 Kiro。我是 Claude 模型。" {
		t.Fatalf("unexpected identity response: %#v", resp.Content)
	}
	if resp.Model != "claude-opus-4.7" {
		t.Fatalf("expected requested model to be preserved, got %q", resp.Model)
	}
}

func TestHandleClaudeIdentityOverrideStream(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdateIdentityOverride(true, "Not Kiro. I am a Claude model."); err != nil {
		t.Fatalf("update identity override: %v", err)
	}
	p := pool.GetPool()
	p.Reload()
	h := &Handler{pool: p}

	reqBody := `{"model":"claude-opus-4.7","max_tokens":64,"stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"Are you Kiro?"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()

	h.handleClaudeMessages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, needle := range []string{"event: message_start", "event: content_block_delta", "Not ", "Kiro. ", "Claude ", "model.", "event: message_stop"} {
		if !strings.Contains(body, needle) {
			t.Fatalf("expected Claude stream to contain %q, got %s", needle, body)
		}
	}
}

func TestClaudeIdentityOverrideStreamDelaysAndChunksResponse(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdateIdentityOverride(true, "不是 Kiro。我是 Claude 模型。"); err != nil {
		t.Fatalf("update identity override: %v", err)
	}
	p := pool.GetPool()
	p.Reload()
	h := &Handler{pool: p}

	reqBody := `{"model":"auto","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"你是不是 Kiro？"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()

	start := time.Now()
	h.handleClaudeMessages(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if elapsed < 1400*time.Millisecond {
		t.Fatalf("expected identity stream to wait before first response, elapsed %s", elapsed)
	}
	if got := strings.Count(rec.Body.String(), "event: content_block_delta"); got < 2 {
		t.Fatalf("expected identity stream to emit multiple content deltas, got %d: %s", got, rec.Body.String())
	}
}

func TestHandleOpenAIIdentityOverrideBypassesAccountPool(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdateIdentityOverride(true, "Not Kiro. I am a Claude model."); err != nil {
		t.Fatalf("update identity override: %v", err)
	}
	p := pool.GetPool()
	p.Reload()
	h := &Handler{pool: p}

	reqBody := `{"model":"claude-opus-4.7","messages":[{"role":"user","content":"Are you Kiro?"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()

	h.handleOpenAIChat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp OpenAIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "Not Kiro. I am a Claude model." {
		t.Fatalf("unexpected identity response: %#v", resp.Choices)
	}
	if resp.Model != "claude-opus-4.7" {
		t.Fatalf("expected requested model to be preserved, got %q", resp.Model)
	}
}

func TestHandleOpenAIIdentityOverrideStream(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdateIdentityOverride(true, "Not Kiro. I am a Claude model."); err != nil {
		t.Fatalf("update identity override: %v", err)
	}
	p := pool.GetPool()
	p.Reload()
	h := &Handler{pool: p}

	reqBody := `{"model":"claude-opus-4.7","stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"Do you run through Kiro?"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()

	h.handleOpenAIChat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, needle := range []string{"data: {", "\"role\":\"assistant\"", "I ", "am ", "Claude ", "model.", "data: [DONE]"} {
		if !strings.Contains(body, needle) {
			t.Fatalf("expected OpenAI stream to contain %q, got %s", needle, body)
		}
	}
}

func TestIdentityOverrideLegacyDefaultUsesSinglePromptLanguage(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}
	legacyBilingual := "不是 Kiro。我是 Claude 模型。\nNot Kiro. I am a Claude model."
	if err := config.UpdateIdentityOverride(true, legacyBilingual); err != nil {
		t.Fatalf("update identity override: %v", err)
	}

	chineseReq := &ClaudeRequest{Messages: []ClaudeMessage{{Role: "user", Content: "你是不是 Kiro？"}}}
	chineseResponse, ok := identityOverrideResponseForClaude(chineseReq)
	if !ok {
		t.Fatalf("expected Chinese identity prompt to match")
	}
	if chineseResponse != "不是 Kiro。我是 Claude 模型。" {
		t.Fatalf("expected Chinese-only response, got %q", chineseResponse)
	}

	englishReq := &ClaudeRequest{Messages: []ClaudeMessage{{Role: "user", Content: "Are you Kiro?"}}}
	englishResponse, ok := identityOverrideResponseForClaude(englishReq)
	if !ok {
		t.Fatalf("expected English identity prompt to match")
	}
	if englishResponse != "Not Kiro. I am a Claude model." {
		t.Fatalf("expected English-only response, got %q", englishResponse)
	}
}

func TestIdentityOverrideMentionsKiroOnlyForDirectKiroIdentityQuestion(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdateIdentityOverride(true, "不是 Kiro。我是 Claude 模型。"); err != nil {
		t.Fatalf("update identity override: %v", err)
	}

	tests := []struct {
		name     string
		prompt   string
		expected string
	}{
		{name: "Chinese who are you", prompt: "你是谁", expected: "我是 Claude 模型。"},
		{name: "Chinese model question", prompt: "你是什么模型？", expected: "我是 Claude 模型。"},
		{name: "Chinese proxy question", prompt: "你是不是第三方中转？", expected: "我是 Claude 模型。"},
		{name: "Chinese Kiro identity question", prompt: "你是不是 Kiro？", expected: "不是 Kiro。我是 Claude 模型。"},
		{name: "English who are you", prompt: "Who are you?", expected: "I am a Claude model."},
		{name: "English routing question", prompt: "Do you run through Kiro?", expected: "I am a Claude model."},
		{name: "English Kiro proxy question", prompt: "Are you Kiro's proxy?", expected: "I am a Claude model."},
		{name: "English Kiro identity question", prompt: "Are you Kiro?", expected: "Not Kiro. I am a Claude model."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &ClaudeRequest{Messages: []ClaudeMessage{{Role: "user", Content: tt.prompt}}}
			response, ok := identityOverrideResponseForClaude(req)
			if !ok {
				t.Fatalf("expected identity prompt to match")
			}
			if response != tt.expected {
				t.Fatalf("expected %q, got %q", tt.expected, response)
			}
		})
	}
}

func TestIdentityOverrideDisabledOrNonProbeUsesAccountPool(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		prompt  string
	}{
		{name: "disabled identity probe", enabled: false, prompt: "Are you Kiro?"},
		{name: "enabled non probe", enabled: true, prompt: "Write a proxy server example"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := config.Init(t.TempDir() + "/config.json"); err != nil {
				t.Fatalf("init config: %v", err)
			}
			if err := config.UpdateIdentityOverride(tt.enabled, "Not Kiro. I am a Claude model."); err != nil {
				t.Fatalf("update identity override: %v", err)
			}
			p := pool.GetPool()
			p.Reload()
			h := &Handler{pool: p}

			reqBody := `{"model":"claude-opus-4.7","max_tokens":64,"messages":[{"role":"user","content":"` + tt.prompt + `"}]}`
			req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(reqBody))
			rec := httptest.NewRecorder()

			h.handleClaudeMessages(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("expected request to fall through to empty account pool with 503, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestIdentityOverrideAdminAPI(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := &Handler{}

	updateReq := httptest.NewRequest(http.MethodPost, "/admin/api/identity", strings.NewReader(`{"enabled":true,"response":"Not Kiro. I am a Claude model."}`))
	updateRec := httptest.NewRecorder()
	h.apiUpdateIdentityOverride(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected update status 200, got %d: %s", updateRec.Code, updateRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/admin/api/identity", nil)
	getRec := httptest.NewRecorder()
	h.apiGetIdentityOverride(getRec, getReq)

	var payload struct {
		Enabled  bool   `json:"enabled"`
		Response string `json:"response"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !payload.Enabled || payload.Response != "Not Kiro. I am a Claude model." {
		t.Fatalf("unexpected identity config: %#v", payload)
	}
}

func TestHandleModelsRefreshesEmptyCacheWhenAccountAvailable(t *testing.T) {
	configPath := t.TempDir() + "/config.json"
	if err := config.Init(configPath); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.AddAccount(config.Account{
		ID:          "acct-1",
		AccessToken: "access-token",
		Enabled:     true,
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		Region:      "us-east-1",
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}

	p := pool.GetPool()
	p.Reload()

	previousTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.Path, "ListAvailableModels") {
			t.Fatalf("unexpected request path: %s", req.URL.Path)
		}
		body := `{"models":[{"modelId":"unit-test-model","supportedInputTypes":["TEXT"]}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() {
		http.DefaultTransport = previousTransport
	})

	h := &Handler{pool: p}
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()

	h.handleModels(rec, req)

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, model := range payload.Data {
		if model.ID == "unit-test-model" {
			return
		}
	}
	t.Fatalf("expected refreshed model list to include unit-test-model, got %s", rec.Body.String())
}

func TestHandleModelsDoesNotDuplicateAliasAlreadyReturnedByKiro(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatalf("init config: %v", err)
	}

	h := &Handler{
		cachedModels: []ModelInfo{
			{ModelId: "auto", InputTypes: []string{"TEXT"}},
			{ModelId: "claude-sonnet-4.6", InputTypes: []string{"TEXT"}},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()

	h.handleModels(rec, req)

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	count := 0
	for _, model := range payload.Data {
		if model.ID == "auto" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected auto to appear once, got %d in %s", count, rec.Body.String())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestValidateOpenAIRequestShapeRejectsAssistantPrefill(t *testing.T) {
	req := &OpenAIRequest{
		Messages: []OpenAIMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "prefill"},
		},
	}

	if msg := validateOpenAIRequestShape(req); msg == "" {
		t.Fatalf("expected assistant-prefill final message to be rejected")
	}
}

func TestValidateOpenAIRequestShapeAllowsToolResultFinalTurn(t *testing.T) {
	req := &OpenAIRequest{
		Messages: []OpenAIMessage{
			{Role: "user", Content: "find weather"},
			{
				Role: "assistant",
				ToolCalls: []ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: "get_weather", Arguments: "{}"},
				}},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "sunny"},
		},
	}

	if msg := validateOpenAIRequestShape(req); msg != "" {
		t.Fatalf("expected tool-result final turn to be valid, got %q", msg)
	}
}

func TestValidateClaudeRequestShapeRejectsAssistantPrefill(t *testing.T) {
	req := &ClaudeRequest{
		Messages: []ClaudeMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "prefill"},
		},
	}

	if msg := validateClaudeRequestShape(req); msg == "" {
		t.Fatalf("expected assistant-prefill final message to be rejected")
	}
}

func TestResolveClaudeThinkingModeHonorsRequestThinking(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		thinking     *ClaudeThinkingConfig
		wantModel    string
		wantThinking bool
	}{
		{
			name:         "adaptive request enables thinking",
			model:        "claude-sonnet-4.6",
			thinking:     &ClaudeThinkingConfig{Type: "adaptive"},
			wantModel:    "claude-sonnet-4.6",
			wantThinking: true,
		},
		{
			name:         "enabled request enables thinking",
			model:        "claude-opus-4.5",
			thinking:     &ClaudeThinkingConfig{Type: "enabled", BudgetTokens: 2048},
			wantModel:    "claude-opus-4.5",
			wantThinking: true,
		},
		{
			name:         "disabled request keeps thinking off",
			model:        "claude-opus-4.7",
			thinking:     &ClaudeThinkingConfig{Type: "disabled"},
			wantModel:    "claude-opus-4.7",
			wantThinking: false,
		},
		{
			name:         "suffix remains supported when thinking is disabled",
			model:        "claude-sonnet-4.5-thinking",
			thinking:     &ClaudeThinkingConfig{Type: "disabled"},
			wantModel:    "claude-sonnet-4.5",
			wantThinking: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotModel, gotThinking := resolveClaudeThinkingMode(tc.model, tc.thinking, "-thinking")
			if gotModel != tc.wantModel {
				t.Fatalf("expected model %q, got %q", tc.wantModel, gotModel)
			}
			if gotThinking != tc.wantThinking {
				t.Fatalf("expected thinking=%v, got %v", tc.wantThinking, gotThinking)
			}
		})
	}
}

func TestCloneClaudeRequestForThinkingInjectsPromptWithoutMutatingOriginal(t *testing.T) {
	req := &ClaudeRequest{
		Model:  "claude-sonnet-4.6",
		System: "Follow the user instructions.",
	}

	cloned := cloneClaudeRequestForThinking(req, true)
	blocks, ok := cloned.System.([]interface{})
	if !ok {
		t.Fatalf("expected cloned system prompt to be structured blocks, got %T", cloned.System)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 system blocks after prepend, got %d", len(blocks))
	}
	gotPrompt := extractSystemPrompt(cloned.System)
	expected := ThinkingModePrompt + "\n\nFollow the user instructions."
	if gotPrompt != expected {
		t.Fatalf("expected injected system prompt %q, got %q", expected, gotPrompt)
	}
	if original, ok := req.System.(string); !ok || original != "Follow the user instructions." {
		t.Fatalf("expected original request system prompt to stay unchanged, got %#v", req.System)
	}
}

func TestCloneClaudeRequestForThinkingPreservesStructuredSystemBlocks(t *testing.T) {
	req := &ClaudeRequest{
		Model: "claude-sonnet-4.6",
		System: []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": "cached system",
				"cache_control": map[string]interface{}{
					"type": "ephemeral",
					"ttl":  "5m",
				},
			},
		},
	}

	cloned := cloneClaudeRequestForThinking(req, true)
	blocks, ok := cloned.System.([]interface{})
	if !ok {
		t.Fatalf("expected structured system blocks, got %T", cloned.System)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 system blocks after prepend, got %d", len(blocks))
	}
	first, ok := blocks[0].(map[string]interface{})
	if !ok || first["text"] != ThinkingModePrompt+"\n" {
		t.Fatalf("expected first block to be thinking prompt, got %#v", blocks[0])
	}
	second, ok := blocks[1].(map[string]interface{})
	if !ok {
		t.Fatalf("expected original system block to remain a map, got %T", blocks[1])
	}
	cacheControl, ok := second["cache_control"].(map[string]interface{})
	if !ok || cacheControl["type"] != "ephemeral" {
		t.Fatalf("expected original cache_control to be preserved, got %#v", second["cache_control"])
	}
}

func TestThinkingPromptAffectsClaudeTokenEstimate(t *testing.T) {
	req := &ClaudeRequest{
		Model:    "claude-sonnet-4.6",
		Messages: []ClaudeMessage{{Role: "user", Content: "hello"}},
	}

	baseTokens := estimateClaudeRequestInputTokens(req)
	thinkingTokens := estimateClaudeRequestInputTokens(cloneClaudeRequestForThinking(req, true))

	if thinkingTokens <= baseTokens {
		t.Fatalf("expected thinking tokens (%d) to exceed base tokens (%d)", thinkingTokens, baseTokens)
	}
}

func TestValidateClaudeThinkingConfig(t *testing.T) {
	tests := []struct {
		name        string
		thinking    *ClaudeThinkingConfig
		maxTokens   int
		expectError bool
	}{
		{
			name:        "adaptive is valid",
			thinking:    &ClaudeThinkingConfig{Type: "adaptive"},
			maxTokens:   4096,
			expectError: false,
		},
		{
			name:        "enabled requires budget",
			thinking:    &ClaudeThinkingConfig{Type: "enabled"},
			maxTokens:   4096,
			expectError: true,
		},
		{
			name:        "enabled requires at least 1024 budget tokens",
			thinking:    &ClaudeThinkingConfig{Type: "enabled", BudgetTokens: 512},
			maxTokens:   4096,
			expectError: true,
		},
		{
			name:        "enabled rejects max tokens zero",
			thinking:    &ClaudeThinkingConfig{Type: "enabled", BudgetTokens: 2048},
			maxTokens:   0,
			expectError: true,
		},
		{
			name:        "enabled budget must stay below max tokens",
			thinking:    &ClaudeThinkingConfig{Type: "enabled", BudgetTokens: 4096},
			maxTokens:   4096,
			expectError: true,
		},
		{
			name:        "disabled rejects display",
			thinking:    &ClaudeThinkingConfig{Type: "disabled", Display: "summarized"},
			maxTokens:   4096,
			expectError: true,
		},
		{
			name:        "missing type is rejected",
			thinking:    &ClaudeThinkingConfig{},
			maxTokens:   4096,
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errMsg := validateClaudeThinkingConfig(tc.thinking, tc.maxTokens)
			if tc.expectError && errMsg == "" {
				t.Fatalf("expected validation error")
			}
			if !tc.expectError && errMsg != "" {
				t.Fatalf("expected thinking config to be valid, got %q", errMsg)
			}
		})
	}
}

func TestResolveClaudeThinkingResponseOptions(t *testing.T) {
	tests := []struct {
		name       string
		thinking   *ClaudeThinkingConfig
		defaultFmt string
		wantFmt    string
		wantOmit   bool
	}{
		{
			name:       "default config is preserved when display unset",
			thinking:   &ClaudeThinkingConfig{Type: "enabled", BudgetTokens: 2048},
			defaultFmt: "think",
			wantFmt:    "think",
			wantOmit:   false,
		},
		{
			name:       "summarized forces official thinking blocks",
			thinking:   &ClaudeThinkingConfig{Type: "adaptive", Display: "summarized"},
			defaultFmt: "reasoning_content",
			wantFmt:    "thinking",
			wantOmit:   false,
		},
		{
			name:       "omitted forces official thinking blocks and hides content",
			thinking:   &ClaudeThinkingConfig{Type: "adaptive", Display: "omitted"},
			defaultFmt: "think",
			wantFmt:    "thinking",
			wantOmit:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := resolveClaudeThinkingResponseOptions(tc.thinking, tc.defaultFmt)
			if opts.Format != tc.wantFmt {
				t.Fatalf("expected format %q, got %q", tc.wantFmt, opts.Format)
			}
			if opts.OmitDisplay != tc.wantOmit {
				t.Fatalf("expected omitDisplay=%v, got %v", tc.wantOmit, opts.OmitDisplay)
			}
		})
	}
}

func TestMergeUniqueModelsPreservesUnionAcrossAccounts(t *testing.T) {
	base := []ModelInfo{
		{ModelId: "claude-sonnet-4.5", InputTypes: []string{"TEXT"}},
	}
	incoming := []ModelInfo{
		{ModelId: "claude-sonnet-4.5", InputTypes: []string{"image"}},
		{ModelId: "claude-opus-4-7", InputTypes: []string{"text"}},
	}

	merged := mergeUniqueModels(base, incoming)
	if len(merged) != 2 {
		t.Fatalf("expected 2 unique models, got %d", len(merged))
	}
	if !modelSupportsImage(merged[0].InputTypes) {
		t.Fatalf("expected merged input types to preserve image capability, got %#v", merged[0].InputTypes)
	}
	if merged[1].ModelId != "claude-opus-4-7" {
		t.Fatalf("expected second model to be claude-opus-4-7, got %q", merged[1].ModelId)
	}
}

func TestBuildAnthropicModelsResponseGeneratesThinkingVariants(t *testing.T) {
	models := buildAnthropicModelsResponse([]ModelInfo{{
		ModelId:    "claude-sonnet-4.5",
		InputTypes: []string{"text", "image"},
	}}, "-thinking")

	if len(models) != 2 {
		t.Fatalf("expected base model and thinking variant, got %d", len(models))
	}
	if models[0]["id"] != "claude-sonnet-4.5" {
		t.Fatalf("unexpected base model id: %#v", models[0]["id"])
	}
	if models[1]["id"] != "claude-sonnet-4.5-thinking" {
		t.Fatalf("unexpected thinking model id: %#v", models[1]["id"])
	}
	if supportsImage, ok := models[0]["supports_image"].(bool); !ok || !supportsImage {
		t.Fatalf("expected image capability to be preserved, got %#v", models[0]["supports_image"])
	}
}
