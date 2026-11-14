package opencode_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"gopkg.in/yaml.v3"
)

// snapshotCases generates the inputs of fixtures that do not exist yet
// (go test ./internal/opencode -run TestSnapshots -update). Once a fixture is
// written it is the source of truth; editing a case here does not change
// an existing fixture, delete the file to regenerate it.

const (
	snapshotVersion = "9.9.9-snapshot"

	// Fake OpenCode keys; none of them is real.
	fakeKey1 = "oc_sk_test_fake_key_0001"
	fakeKey2 = "oc_sk_test_fake_key_0002"
	fakeKey3 = "oc_sk_test_fake_key_0003"
	fakeKey4 = "oc_sk_test_fake_key_0004"

	goBase       = "/inference/go"
	pathChat     = goBase + "/openai/v1/chat/completions"
	pathResp     = goBase + "/openai/v1/responses"
	pathMessages = goBase + "/anthropic/v1/messages"
	pathCount    = goBase + "/anthropic/v1/messages/count_tokens"
	pathModels   = goBase + "/v1/models"
	pathGoUsage  = "/zen/go/v1/usage"

	mgmtBase = "/v0/management/plugins/opencode"
)

type snapshotCase struct {
	name        string
	description string
	// config is the plugin config object; nil means snapshotConfig().
	config      map[string]any
	credentials []caseCredential
	script      []snapshotReply
	steps       []caseStep
}

// caseCredential is an auth file seeded in the fake host; index must be the
// auth index the host gives it (idx-<position>).
type caseCredential struct {
	name   string
	index  string
	fields map[string]any
}

func (c caseCredential) raw() []byte {
	raw, _ := json.Marshal(c.fields)
	return raw
}

func (c caseCredential) metadata() map[string]any {
	out := make(map[string]any, len(c.fields))
	for key, value := range c.fields {
		out[key] = value
	}
	return out
}

type caseStep struct {
	method  string
	request any
}

func (c snapshotCase) fixture(t *testing.T) snapshotFixture {
	t.Helper()
	config := c.config
	if config == nil {
		config = snapshotConfig(nil)
	}
	fixture := snapshotFixture{
		Case:           c.name,
		Description:    c.description,
		Setup:          snapshotSetup{Version: snapshotVersion, Config: caseJSON(t, config), Credentials: []snapshotCredential{}},
		UpstreamScript: []snapshotReply{},
		Steps:          []snapshotStep{},
	}
	for i, cred := range c.credentials {
		if want := fmt.Sprintf("idx-%d", i+1); cred.index != want {
			t.Fatalf("case %s: credential %s has index %s, want %s", c.name, cred.name, cred.index, want)
		}
		fixture.Setup.Credentials = append(fixture.Setup.Credentials, snapshotCredential{Name: cred.name, Fields: caseJSON(t, cred.fields)})
	}
	for _, reply := range c.script {
		if reply.Header == nil {
			reply.Header = http.Header{}
		}
		if reply.Chunks == nil {
			reply.Chunks = []string{}
		}
		fixture.UpstreamScript = append(fixture.UpstreamScript, reply)
	}
	for _, step := range c.steps {
		fixture.Steps = append(fixture.Steps, snapshotStep{RPC: snapshotRPC{Method: step.method, Request: caseJSON(t, step.request)}})
	}
	return fixture
}

func snapshotConfig(extra map[string]any) map[string]any {
	config := map[string]any{
		"enabled":  true,
		"priority": 0,
		"base-url": "{{UPSTREAM}}" + goBase,
		"data-dir": "{{DATA_DIR}}",
	}
	for key, value := range extra {
		config[key] = value
	}
	return config
}

// RPC request shapes as CPA sends them (CPA's rpc_schema.go).

type cpaExecutorRequest struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type cpaManagementRequest struct {
	pluginapi.ManagementRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type cpaQuotaFetchRequest struct {
	pluginapi.QuotaFetchRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type cpaAuthRefreshRequest struct {
	pluginapi.AuthRefreshRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type cpaRequestInterceptRequest struct {
	pluginapi.RequestInterceptRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

const hostCallbackID = "host-callback-1"

func opencodeCred(index int, name string, fields map[string]any) caseCredential {
	all := map[string]any{"type": "opencode"}
	for key, value := range fields {
		all[key] = value
	}
	return caseCredential{name: name, index: fmt.Sprintf("idx-%d", index), fields: all}
}

func credK1(index int) caseCredential {
	return opencodeCred(index, "opencode-k1.json", map[string]any{"api_key": fakeKey1})
}

type execOption func(*cpaExecutorRequest)

// execRequest builds an executor request the way CPA does after routing a
// client request with model "opencode/<model>" to cred.
func execRequest(cred caseCredential, format, payload string, options ...execOption) cpaExecutorRequest {
	var body struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal([]byte(payload), &body)
	requested := body.Model
	if requested != "" && !strings.Contains(requested, "/") {
		requested = "opencode/" + requested
	}
	original := strings.Replace(payload, `"model":"`+body.Model+`"`, `"model":"`+requested+`"`, 1)
	paths := map[string]string{"openai": "/v1/chat/completions", "openai-response": "/v1/responses", "claude": "/v1/messages"}
	req := cpaExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			AuthID:       cred.name,
			AuthProvider: "opencode",
			Model:        body.Model,
			Format:       format,
			Headers: http.Header{
				"Accept":        {"application/json"},
				"Authorization": {"Bearer sk-cpa-client-fake"},
				"Content-Type":  {"application/json"},
				"User-Agent":    {"snapshot-client/1.0"},
			},
			OriginalRequest: []byte(original),
			SourceFormat:    format,
			Payload:         []byte(payload),
			Metadata: map[string]any{
				"request_path":        paths[format],
				"requested_model":     requested,
				"selected_auth_id":    cred.name,
				"selected_auth_index": cred.index,
			},
			StorageJSON:  cred.raw(),
			AuthMetadata: cred.metadata(),
		},
		HostCallbackID: hostCallbackID,
	}
	for _, option := range options {
		option(&req)
	}
	return req
}

func streaming(streamID string) execOption {
	return func(req *cpaExecutorRequest) {
		req.Stream = true
		req.StreamID = streamID
		req.Headers.Set("Accept", "text/event-stream")
	}
}

func withHeader(name, value string) execOption {
	return func(req *cpaExecutorRequest) { req.Headers.Set(name, value) }
}

func withModel(model string) execOption {
	return func(req *cpaExecutorRequest) { req.Model = model }
}

func mgmtRequest(method, route string, query url.Values, body string) cpaManagementRequest {
	req := cpaManagementRequest{
		ManagementRequest: pluginapi.ManagementRequest{
			Method:  method,
			Path:    route,
			Headers: http.Header{"Accept": {"application/json"}},
			Query:   query,
		},
		HostCallbackID: hostCallbackID,
	}
	if body != "" {
		req.Headers.Set("Content-Type", "application/json")
		req.Body = []byte(body)
	}
	return req
}

func mgmt(method, route string, body string) caseStep {
	return caseStep{method: pluginabi.MethodManagementHandle, request: mgmtRequest(method, route, nil, body)}
}

func mgmtQuery(method, route string, query url.Values) caseStep {
	return caseStep{method: pluginabi.MethodManagementHandle, request: mgmtRequest(method, route, query, "")}
}

func execStep(method string, req cpaExecutorRequest) caseStep {
	return caseStep{method: method, request: req}
}

func modelsForAuthStep(cred caseCredential, force bool) caseStep {
	return caseStep{method: pluginabi.MethodModelForAuth, request: cpaAuthModelRequest{AuthModelRequest: pluginapi.AuthModelRequest{
		AuthID:       cred.name,
		AuthProvider: "opencode",
		StorageJSON:  cred.raw(),
		Metadata:     cred.metadata(),
		Host:         pluginapi.HostConfigSummary{AuthDir: snapshotAuthDir, ForceModelPrefix: force},
	}, HostCallbackID: hostCallbackID}}
}

func quotaStep(cred caseCredential, withStorage bool) caseStep {
	req := cpaQuotaFetchRequest{QuotaFetchRequest: pluginapi.QuotaFetchRequest{AuthIndex: cred.index, AuthID: cred.name, Provider: "opencode"}, HostCallbackID: hostCallbackID}
	if withStorage {
		req.StorageJSON = cred.raw()
		req.Metadata = cred.metadata()
	}
	return caseStep{method: pluginabi.MethodQuotaFetch, request: req}
}

func lifecycleStep(method string, schema uint32, config map[string]any) caseStep {
	raw, _ := json.Marshal(config)
	var plain map[string]any
	_ = json.Unmarshal(raw, &plain)
	text, errYAML := snapshotYAML(plain)
	if errYAML != nil {
		panic(errYAML)
	}
	return caseStep{method: method, request: cpaLifecycleRequest{ConfigYAML: text, SchemaVersion: schema}}
}

func bare(method string) caseStep {
	return caseStep{method: method}
}

func reply(method, path string, status int, contentType, body string) snapshotReply {
	header := http.Header{}
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return snapshotReply{Method: method, Path: path, Status: status, Header: header, Body: body}
}

func jsonReply(path string, status int, body string) snapshotReply {
	return reply(http.MethodPost, path, status, "application/json", body)
}

func withReplyHeader(r snapshotReply, name, value string) snapshotReply {
	r.Header = r.Header.Clone()
	r.Header.Set(name, value)
	return r
}

func sseReply(path string, events ...string) snapshotReply {
	r := reply(http.MethodPost, path, http.StatusOK, "text/event-stream", "")
	for _, event := range events {
		r.Chunks = append(r.Chunks, event+"\n\n")
	}
	return r
}

func getReply(path string, status int, body string) snapshotReply {
	return reply(http.MethodGet, path, status, "application/json", body)
}

// Canned upstream bodies.

const (
	chatPayload       = `{"model":"kimi-k3","messages":[{"role":"user","content":"Say hello"}],"max_tokens":64,"temperature":0.2}`
	chatStreamPayload = `{"model":"kimi-k3","messages":[{"role":"user","content":"Say hello"}],"max_tokens":64,"stream":true}`
	respPayload       = `{"model":"kimi-k3","instructions":"Be brief.","input":[{"role":"user","content":"Say hello"}],"reasoning":{"effort":"high"},"max_output_tokens":256}`
	respStreamPayload = `{"model":"kimi-k3","instructions":"Be brief.","input":[{"role":"user","content":"Say hello"}],"stream":true}`
	msgPayload        = `{"model":"kimi-k3","max_tokens":256,"system":"Be brief.","messages":[{"role":"user","content":"Say hello"}],"thinking":{"type":"enabled","budget_tokens":1024}}`
	msgStreamPayload  = `{"model":"kimi-k3","max_tokens":256,"system":"Be brief.","messages":[{"role":"user","content":"Say hello"}],"stream":true,"output_config":{"effort":"medium"}}`
	countPayload      = `{"model":"kimi-k3","system":"Be brief.","messages":[{"role":"user","content":"Count me"}]}`

	chatBody = `{"id":"chatcmpl-snap-1","object":"chat.completion","created":1767225600,"model":"kimi-k3","choices":[{"index":0,"message":{"role":"assistant","content":"Hello there!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":4,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":1}}}`
	respBody = `{"id":"resp_snap_1","object":"response","created_at":1767225600,"status":"completed","model":"kimi-k3","output":[{"type":"reasoning","id":"rs_snap_1","summary":[]},{"type":"message","id":"msg_snap_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello there!","annotations":[]}]}],"usage":{"input_tokens":20,"input_tokens_details":{"cached_tokens":8},"output_tokens":6,"output_tokens_details":{"reasoning_tokens":2},"total_tokens":26}}`
	msgBody  = `{"id":"msg_snap_1","type":"message","role":"assistant","model":"kimi-k3","content":[{"type":"thinking","thinking":"A greeting.","signature":"sig-fake"},{"type":"text","text":"Hello there!"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":12,"cache_creation_input_tokens":5,"cache_read_input_tokens":7,"output_tokens":9,"output_tokens_details":{"thinking_tokens":3}}}`

	catalogBody = `{"object":"list","data":[` +
		`{"id":"kimi-k3","object":"model","created":1767225600,"name":"Kimi K3","type":"chat","description":"Moonshot Kimi K3","family":"kimi","open_weights":true,"release_date":"2026-01-01","modalities":{"input":["text","image/png","application/pdf"],"output":["text"]},"capabilities":{"tools":{"supported":true},"reasoning":{"supported":true,"effort":["none","low","high"]}},"limit":{"context":262144,"output":32768},"cost":{"text":{"input":{"price":0.6,"cache":{"read":0.15,"write":{"30m":0.75,"12h":1.2}},"overrides":[{"when":{"context":{"gt":128000}},"price":1.2}]},"output":{"price":2.5,"overrides":[{"when":{"context":{"gt":128000}},"price":5}]}},"multipliers":[{"when":{"hours":"peak"},"factor":1.5}]},"api":{"messages":{},"chat-completions":{}}},` +
		`{"id":"glm-5","object":"model","created":1767312000,"name":"GLM 5","type":"chat","family":"glm","modalities":{"input":["text"]},"capabilities":{"tools":{"supported":true},"reasoning":{"supported":false}},"limit":{"context":200000,"output":16384},"cost":{"text":{"input":{"price":0},"output":{"price":0}}},"api":{"chat-completions":{}}},` +
		`{"id":"text-embed-1","object":"model","created":1767398400,"name":"Text Embed 1","type":"embedding","modalities":{"input":["text"]},"limit":{"context":8192,"output":0}},` +
		`{"id":"kimi-k3","object":"model","created":1,"name":"Duplicate entry"}]}`

	goUsageBody = `{"usage":{"rolling":{"status":"ok","percent":12.5,"resetsAt":"2026-01-02T07:00:00Z"},"weekly":{"status":"rate-limited","percent":100,"resetsAt":"2026-01-05T00:00:00Z"},"monthly":{"status":"ok","percent":40,"resetsAt":"2026-01-20T09:30:00Z"}}}`
)

func chatChunk(delta string, finish string) string {
	choice := `{"index":0,"delta":` + delta
	if finish != "" {
		choice += `,"finish_reason":"` + finish + `"`
	}
	return `data: {"id":"chatcmpl-snap-2","object":"chat.completion.chunk","created":1767225600,"model":"kimi-k3","choices":[` + choice + `}]}`
}

var (
	chatStreamEvents = []string{
		": OPENCODE PROCESSING",
		chatChunk(`{"role":"assistant","content":""}`, ""),
		chatChunk(`{"content":"Hello"}`, ""),
		chatChunk(`{"content":" there!"}`, ""),
		chatChunk(`{}`, "stop"),
		`data: {"id":"chatcmpl-snap-2","object":"chat.completion.chunk","created":1767225600,"model":"kimi-k3","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":4,"total_tokens":15}}`,
		"data: [DONE]",
	}
	respStreamEvents = []string{
		"event: response.created\n" + `data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_snap_2","object":"response","created_at":1767225600,"status":"in_progress","model":"kimi-k3","output":[]}}`,
		"event: response.output_item.added\n" + `data: {"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"type":"message","id":"msg_snap_2","status":"in_progress","role":"assistant","content":[]}}`,
		"event: response.output_text.delta\n" + `data: {"type":"response.output_text.delta","sequence_number":2,"item_id":"msg_snap_2","output_index":0,"content_index":0,"delta":"Hello there!"}`,
		"event: response.completed\n" + `data: {"type":"response.completed","sequence_number":3,"response":{"id":"resp_snap_2","object":"response","created_at":1767225600,"status":"completed","model":"kimi-k3","output":[{"type":"message","id":"msg_snap_2","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello there!","annotations":[]}]}],"usage":{"input_tokens":20,"output_tokens":6,"total_tokens":26,"output_tokens_details":{"reasoning_tokens":0}}}}`,
	}
	msgStreamEvents = []string{
		"event: message_start\n" + `data: {"type":"message_start","message":{"id":"msg_snap_2","type":"message","role":"assistant","model":"kimi-k3","content":[],"stop_reason":null,"usage":{"input_tokens":12,"cache_creation_input_tokens":0,"cache_read_input_tokens":7,"output_tokens":1}}}`,
		"event: ping\n" + `data: {"type":"ping"}`,
		"event: content_block_start\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"event: content_block_delta\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello there!"}}`,
		"event: content_block_stop\n" + `data: {"type":"content_block_stop","index":0}`,
		"event: message_delta\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":9}}`,
		"event: message_stop\n" + `data: {"type":"message_stop"}`,
	}
)

func snapshotCases() []snapshotCase {
	k1 := credK1(1)
	var cases []snapshotCase
	cases = append(cases, execCases(k1)...)
	cases = append(cases, errorCases(k1)...)
	cases = append(cases, countTokensCases(k1)...)
	cases = append(cases, sessionCases(k1)...)
	cases = append(cases, catalogCases()...)
	cases = append(cases, credentialCases()...)
	cases = append(cases, quotaCases()...)
	cases = append(cases, managementCases(k1)...)
	cases = append(cases, lifecycleCases()...)
	return cases
}

func execCases(k1 caseCredential) []snapshotCase {
	k2 := opencodeCred(2, "opencode-k2.json", map[string]any{"api_key": fakeKey2, "base_url": "{{UPSTREAM}}/inference", "prefix": "oc"})
	return []snapshotCase{
		{
			name:        "exec_chat_completions_nonstream",
			description: "OpenAI chat completions, buffered: key as bearer, client credentials dropped, session from the first messages, usage passed through",
			credentials: []caseCredential{k1},
			script:      []snapshotReply{withReplyHeader(jsonReply(pathChat, 200, chatBody), "X-Request-Id", "oc-req-chat-1")},
			steps:       []caseStep{execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "openai", chatPayload))},
		},
		{
			name:        "exec_chat_completions_stream",
			description: "OpenAI chat completions over the host stream bridge: stream_options.include_usage added, keepalive dropped, [DONE] kept",
			credentials: []caseCredential{k1},
			script:      []snapshotReply{withReplyHeader(sseReply(pathChat, chatStreamEvents...), "X-Request-Id", "oc-req-chat-2")},
			steps:       []caseStep{execStep(pluginabi.MethodExecutorExecuteStream, execRequest(k1, "openai", chatStreamPayload, streaming("stream-chat-1")))},
		},
		{
			name:        "exec_responses_nonstream",
			description: "OpenAI Responses, buffered, with reasoning effort and usage",
			credentials: []caseCredential{k1},
			script:      []snapshotReply{withReplyHeader(jsonReply(pathResp, 200, respBody), "Request-Id", "oc-req-resp-1")},
			steps:       []caseStep{execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "openai-response", respPayload))},
		},
		{
			name:        "exec_responses_stream",
			description: "OpenAI Responses over the host stream bridge; prelude events held until the first delta",
			credentials: []caseCredential{k1},
			script:      []snapshotReply{sseReply(pathResp, respStreamEvents...)},
			steps:       []caseStep{execStep(pluginabi.MethodExecutorExecuteStream, execRequest(k1, "openai-response", respStreamPayload, streaming("stream-resp-1")))},
		},
		{
			name:        "exec_messages_nonstream",
			description: "Anthropic Messages, buffered: Anthropic-Version and Anthropic-Beta forwarded, cache and thinking usage",
			credentials: []caseCredential{k1},
			script:      []snapshotReply{withReplyHeader(jsonReply(pathMessages, 200, msgBody), "Request-Id", "oc-req-msg-1")},
			steps: []caseStep{execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "claude", msgPayload,
				withHeader("Anthropic-Version", "2023-06-01"), withHeader("Anthropic-Beta", "interleaved-thinking-2025-05-14"), withHeader("X-Api-Key", "sk-cpa-client-fake")))},
		},
		{
			name:        "exec_messages_stream",
			description: "Anthropic Messages over the host stream bridge; message_start, ping and content_block_start held until the first delta",
			credentials: []caseCredential{k1},
			script:      []snapshotReply{sseReply(pathMessages, msgStreamEvents...)},
			steps:       []caseStep{execStep(pluginabi.MethodExecutorExecuteStream, execRequest(k1, "claude", msgStreamPayload, streaming("stream-msg-1"), withHeader("Anthropic-Version", "2023-06-01")))},
		},
		{
			name:        "exec_request_rewrites",
			description: "Model prefix and thinking suffix stripped, credential base_url and prefix, Anthropic headers dropped on OpenAI endpoints, default Anthropic-Version, stream_options kept, buffered stream fallback without a stream_id",
			credentials: []caseCredential{k1, k2},
			script: []snapshotReply{
				jsonReply(pathChat, 200, chatBody),
				sseReply("/inference/openai/v1/chat/completions", chatStreamEvents...),
				jsonReply(pathMessages, 200, msgBody),
				sseReply(pathResp, respStreamEvents...),
			},
			steps: []caseStep{
				execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "openai", `{"model":"opencode/kimi-k3(high)","messages":[{"role":"user","content":"Say hello"}]}`,
					withModel("opencode/kimi-k3(high)"), withHeader("Anthropic-Version", "2023-06-01"), withHeader("Anthropic-Beta", "some-beta"))),
				execStep(pluginabi.MethodExecutorExecuteStream, execRequest(k2, "openai", `{"stream_options":{"include_usage":false},"model":"oc/glm-5","messages":[{"role":"user","content":"Say hello"}],"stream":true}`,
					withModel("oc/glm-5"), streaming("stream-rewrite-1"))),
				execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "claude", `{"model":"kimi-k3","max_tokens":32,"messages":[{"role":"user","content":"Say hello"}]}`)),
				execStep(pluginabi.MethodExecutorExecuteStream, execRequest(k1, "openai-response", respStreamPayload, streaming(""))),
			},
		},
	}
}

func errorCases(k1 caseCredential) []snapshotCase {
	chat := func() cpaExecutorRequest { return execRequest(k1, "openai", chatPayload) }
	noKey := opencodeCred(2, "opencode-nokey.json", map[string]any{})
	badProxy := opencodeCred(3, "opencode-badproxy.json", map[string]any{"api_key": fakeKey3, "proxy_url": "ftp://10.0.0.1:21"})
	return []snapshotCase{
		{
			name:        "error_400",
			description: "Upstream 400 with an OpenAI style error body",
			credentials: []caseCredential{k1},
			script:      []snapshotReply{jsonReply(pathChat, 400, `{"error":{"message":"Invalid value for max_tokens","type":"invalid_request_error","code":"invalid_value"}}`)},
			steps:       []caseStep{execStep(pluginabi.MethodExecutorExecute, chat())},
		},
		{
			name:        "error_401",
			description: "Upstream 401 with an Anthropic style error body on Messages",
			credentials: []caseCredential{k1},
			script:      []snapshotReply{jsonReply(pathMessages, 401, `{"type":"error","error":{"type":"authentication_error","message":"Invalid API key."}}`)},
			steps:       []caseStep{execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "claude", msgPayload, withHeader("Anthropic-Version", "2023-06-01")))},
		},
		{
			name:        "error_403",
			description: "Upstream 403 on Responses keeps its status",
			credentials: []caseCredential{k1},
			script:      []snapshotReply{jsonReply(pathResp, 403, `{"error":{"type":"permission_error","message":"Model is blocked by the workspace privacy settings"}}`)},
			steps:       []caseStep{execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "openai-response", respPayload))},
		},
		{
			name:        "error_429_retry_after",
			description: "Upstream 429 with Retry-After, buffered and on a stream request (failed call before the stream starts)",
			credentials: []caseCredential{k1},
			script: []snapshotReply{
				withReplyHeader(jsonReply(pathChat, 429, `{"error":{"type":"rate_limit_error","code":"budget_exceeded","message":"Workspace monthly budget exceeded"}}`), "Retry-After", "30"),
				withReplyHeader(jsonReply(pathChat, 429, `{"error":{"message":"Go usage limit reached"}}`), "Retry-After", "7"),
			},
			steps: []caseStep{
				execStep(pluginabi.MethodExecutorExecute, chat()),
				execStep(pluginabi.MethodExecutorExecuteStream, execRequest(k1, "openai", chatStreamPayload, streaming("stream-429-1"))),
			},
		},
		{
			name:        "error_500",
			description: "Upstream 500 is retryable",
			credentials: []caseCredential{k1},
			script:      []snapshotReply{withReplyHeader(jsonReply(pathChat, 500, `{"error":{"message":"internal server error"}}`), "X-Request-Id", "oc-req-500")},
			steps:       []caseStep{execStep(pluginabi.MethodExecutorExecute, chat())},
		},
		{
			name:        "error_plain_text_body",
			description: "Upstream 502 with a plain-text body becomes the message",
			credentials: []caseCredential{k1},
			script:      []snapshotReply{reply(http.MethodPost, pathChat, 502, "text/plain; charset=utf-8", "Bad Gateway: upstream connect error\n")},
			steps:       []caseStep{execStep(pluginabi.MethodExecutorExecute, chat())},
		},
		{
			name:        "error_string_error_body",
			description: `Upstream 400 with a plain {"error":"msg"} body`,
			credentials: []caseCredential{k1},
			script:      []snapshotReply{jsonReply(pathChat, 400, `{"error":"model is required"}`)},
			steps:       []caseStep{execStep(pluginabi.MethodExecutorExecute, chat())},
		},
		{
			name:        "error_status_mapping",
			description: "404 becomes 400, 402 is kept, 529 becomes 503, 499 becomes 408",
			credentials: []caseCredential{k1},
			script: []snapshotReply{
				jsonReply(pathChat, 404, `{"error":{"type":"not_found_error","message":"Model kimi-k9 not found"}}`),
				jsonReply(pathChat, 402, `{"error":{"code":"insufficient_balance","message":"Workspace is out of credits"}}`),
				jsonReply(pathChat, 529, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`),
				jsonReply(pathChat, 499, `{"code":"downstream_aborted","message":"client went away"}`),
			},
			steps: []caseStep{
				execStep(pluginabi.MethodExecutorExecute, chat()),
				execStep(pluginabi.MethodExecutorExecute, chat()),
				execStep(pluginabi.MethodExecutorExecute, chat()),
				execStep(pluginabi.MethodExecutorExecute, chat()),
			},
		},
		{
			name:        "error_stream_midway",
			description: "A budget error event after output started reaches the client as is, the stream closes normally and the request log row",
			credentials: []caseCredential{k1},
			script: []snapshotReply{sseReply(pathChat,
				chatChunk(`{"role":"assistant","content":"Partial"}`, ""),
				`data: {"error":{"type":"budget_exceeded","code":"budget_exceeded","message":"Workspace monthly budget exceeded"}}`,
			)},
			steps: []caseStep{
				execStep(pluginabi.MethodExecutorExecuteStream, execRequest(k1, "openai", chatStreamPayload, streaming("stream-midway-1"))),
				mgmt(http.MethodGet, mgmtBase+"/logs", ""),
			},
		},
		{
			name:        "error_stream_before_output",
			description: "Error events before any output close the stream with a mapped error: rate limit/budget (429 + Retry-After), insufficient quota (402), response.failed usage limit (429), overloaded (529 -> 503)",
			credentials: []caseCredential{k1},
			script: []snapshotReply{
				withReplyHeader(sseReply(pathMessages, msgStreamEvents[0], msgStreamEvents[1],
					"event: error\n"+`data: {"type":"error","error":{"type":"rate_limit_error","message":"Go usage limit reached: budget exceeded for the 5-hour window"}}`), "Retry-After", "120"),
				sseReply(pathChat, chatChunk(`{"role":"assistant"}`, ""), `data: {"error":{"code":"insufficient_quota","message":"Insufficient balance"}}`),
				sseReply(pathResp, respStreamEvents[0],
					"event: response.failed\n"+`data: {"type":"response.failed","sequence_number":1,"response":{"id":"resp_snap_3","status":"failed","error":{"code":"usage_limit_reached","message":"Go usage limit reached"}}}`),
				sseReply(pathChat, `data: {"error":{"type":"overloaded","message":"Model is overloaded"}}`),
			},
			steps: []caseStep{
				execStep(pluginabi.MethodExecutorExecuteStream, execRequest(k1, "claude", msgStreamPayload, streaming("stream-before-1"))),
				execStep(pluginabi.MethodExecutorExecuteStream, execRequest(k1, "openai", chatStreamPayload, streaming("stream-before-2"))),
				execStep(pluginabi.MethodExecutorExecuteStream, execRequest(k1, "openai-response", respStreamPayload, streaming("stream-before-3"))),
				execStep(pluginabi.MethodExecutorExecuteStream, execRequest(k1, "openai", chatStreamPayload, streaming("stream-before-4"))),
				mgmt(http.MethodGet, mgmtBase+"/logs", ""),
			},
		},
		{
			name:        "error_stream_truncated",
			description: "Streams that end without their terminal event close as stream_truncated",
			credentials: []caseCredential{k1},
			script: []snapshotReply{
				sseReply(pathResp, respStreamEvents[0], respStreamEvents[1], respStreamEvents[2]),
				sseReply(pathChat, chatChunk(`{"role":"assistant","content":"Hel"}`, "")),
			},
			steps: []caseStep{
				execStep(pluginabi.MethodExecutorExecuteStream, execRequest(k1, "openai-response", respStreamPayload, streaming("stream-trunc-1"))),
				execStep(pluginabi.MethodExecutorExecuteStream, execRequest(k1, "openai", chatStreamPayload, streaming("stream-trunc-2"))),
				mgmt(http.MethodGet, mgmtBase+"/logs", ""),
			},
		},
		{
			name:        "error_rejected_before_upstream",
			description: "Requests the plugin refuses without calling OpenCode (no model, unknown format, no api_key, bad proxy) and their request log rows",
			credentials: []caseCredential{k1, noKey, badProxy},
			steps: []caseStep{
				execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "openai", `{"messages":[{"role":"user","content":"no model"}]}`)),
				execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "gemini", chatPayload)),
				execStep(pluginabi.MethodExecutorExecuteStream, execRequest(noKey, "openai", chatStreamPayload, streaming("stream-nokey-1"))),
				execStep(pluginabi.MethodExecutorExecute, execRequest(badProxy, "openai", chatPayload)),
				execStep(pluginabi.MethodExecutorCountTokens, execRequest(noKey, "claude", countPayload)),
				mgmt(http.MethodGet, mgmtBase+"/logs", ""),
			},
		},
	}
}

func countTokensCases(k1 caseCredential) []snapshotCase {
	count := func() cpaExecutorRequest {
		return execRequest(k1, "claude", countPayload, withHeader("Anthropic-Version", "2023-06-01"), withHeader("Anthropic-Beta", "token-counting-2024-11-01"))
	}
	return []snapshotCase{
		{
			name:        "count_tokens_ok",
			description: "count_tokens is forwarded to /anthropic/v1/messages/count_tokens and not written to the request log",
			credentials: []caseCredential{k1},
			script:      []snapshotReply{jsonReply(pathCount, 200, `{"input_tokens":42}`)},
			steps: []caseStep{
				execStep(pluginabi.MethodExecutorCountTokens, count()),
				mgmt(http.MethodGet, mgmtBase+"/logs", ""),
			},
		},
		{
			name:        "count_tokens_error",
			description: "count_tokens upstream errors: 400 kept, 404 mapped to 400",
			credentials: []caseCredential{k1},
			script: []snapshotReply{
				jsonReply(pathCount, 400, `{"type":"error","error":{"type":"invalid_request_error","message":"messages: at least one message is required"}}`),
				reply(http.MethodPost, pathCount, 404, "text/plain", "404 page not found"),
			},
			steps: []caseStep{
				execStep(pluginabi.MethodExecutorCountTokens, count()),
				execStep(pluginabi.MethodExecutorCountTokens, count()),
			},
		},
	}
}

func sessionCases(k1 caseCredential) []snapshotCase {
	chatWith := func(payload string, options ...execOption) caseStep {
		return execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "openai", payload, options...))
	}
	cacheKeyPayload := `{"model":"kimi-k3","prompt_cache_key":"pck-ignored","messages":[{"role":"user","content":"Say hello"}]}`
	spaced := opencodeCred(1, "opencode k1 spaced.json", map[string]any{"api_key": fakeKey1})
	return []snapshotCase{
		{
			name:        "session_client_header",
			description: "X-Opencode-Session from the client's session headers, in order of preference; an over-long value is skipped",
			credentials: []caseCredential{k1},
			script:      repeatReply(jsonReply(pathChat, 200, chatBody), 5),
			steps: []caseStep{
				chatWith(cacheKeyPayload, withHeader("X-Opencode-Session", "ses_opencode_1"), withHeader("X-Claude-Code-Session-Id", "cc-ignored")),
				chatWith(cacheKeyPayload, withHeader("X-Claude-Code-Session-Id", "cc-session-2"), withHeader("Session_id", "codex-ignored")),
				chatWith(chatPayload, withHeader("Session_id", "codex-session-3")),
				chatWith(chatPayload, withHeader("Conversation-Id", "conversation-4")),
				chatWith(chatPayload, withHeader("X-Opencode-Session", strings.Repeat("x", 300)), withHeader("X-Session-Id", "x-session-5")),
			},
		},
		{
			name:        "session_prompt_cache_key",
			description: "X-Opencode-Session from prompt_cache_key on Responses and chat completions",
			credentials: []caseCredential{k1},
			script:      []snapshotReply{jsonReply(pathResp, 200, respBody), jsonReply(pathChat, 200, chatBody)},
			steps: []caseStep{
				execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "openai-response", `{"model":"kimi-k3","prompt_cache_key":"pck-resp-1","input":"Say hello"}`)),
				chatWith(`{"model":"kimi-k3","messages":[{"role":"user","content":"Say hello"}],"prompt_cache_key":"pck-chat-2"}`),
			},
		},
		{
			name:        "session_claude_code_metadata",
			description: "X-Opencode-Session from Claude Code metadata.user_id (legacy string and JSON forms); a user_id without a session falls back to the conversation hash",
			credentials: []caseCredential{k1},
			script:      repeatReply(jsonReply(pathMessages, 200, msgBody), 3),
			steps: []caseStep{
				execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "claude", `{"model":"kimi-k3","max_tokens":64,"metadata":{"user_id":"user_0123abcd_account_4567efgh_session_9f1c2d3e-0000-4000-8000-000000000001"},"messages":[{"role":"user","content":"Say hello"}]}`)),
				execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "claude", `{"model":"kimi-k3","max_tokens":64,"metadata":{"user_id":"{\"device_id\":\"dev-1\",\"account_uuid\":\"acc-1\",\"session_id\":\"9f1c2d3e-0000-4000-8000-000000000002\"}"},"messages":[{"role":"user","content":"Say hello"}]}`)),
				execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "claude", `{"model":"kimi-k3","max_tokens":64,"metadata":{"user_id":"user_0123abcd"},"system":"Be brief.","messages":[{"role":"user","content":"Say hello"}]}`)),
			},
		},
		{
			name:        "session_conversation_hash",
			description: "Without a client session, a hash of the system prompt and the messages up to the first user message; later turns and moved cache_control keep the same session",
			credentials: []caseCredential{k1},
			script:      append(repeatReply(jsonReply(pathMessages, 200, msgBody), 2), jsonReply(pathResp, 200, respBody), jsonReply(pathResp, 200, respBody)),
			steps: []caseStep{
				execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "claude", `{"model":"kimi-k3","max_tokens":64,"system":[{"type":"text","text":"You are helpful.","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"Hello","cache_control":{"type":"ephemeral"}}]}]}`)),
				execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "claude", `{"model":"kimi-k3","max_tokens":64,"system":[{"type":"text","text":"You are helpful."}],"messages":[{"role":"user","content":[{"type":"text","text":"Hello"}]},{"role":"assistant","content":"Hi!"},{"role":"user","content":[{"type":"text","text":"How are you?","cache_control":{"type":"ephemeral"}}]}]}`)),
				execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "openai-response", `{"model":"kimi-k3","instructions":"Be brief.","input":[{"role":"developer","content":"Rules"},{"role":"user","content":"Hello"},{"role":"assistant","content":"Hi"}]}`)),
				execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "openai-response", `{"model":"kimi-k3","input":"Hello"}`)),
			},
		},
		{
			name:        "session_auth_fallback",
			description: "Without any session source the session is cpa_<auth ID> with spaces replaced",
			credentials: []caseCredential{spaced},
			script:      []snapshotReply{jsonReply(pathChat, 200, chatBody)},
			steps:       []caseStep{execStep(pluginabi.MethodExecutorExecute, execRequest(spaced, "openai", `{"model":"kimi-k3","prompt":"Say hello"}`))},
		},
	}
}

func repeatReply(r snapshotReply, n int) []snapshotReply {
	out := make([]snapshotReply, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, r)
	}
	return out
}

func catalogCases() []snapshotCase {
	k1 := credK1(1)
	k2 := opencodeCred(2, "opencode-k2.json", map[string]any{"api_key": fakeKey2, "prefix": "oc", "models": []string{"retired-model", "kimi-k3"}})
	disabled := opencodeCred(2, "opencode-off.json", map[string]any{"api_key": fakeKey2, "disabled": true, "model_revision": "0000000000000000"})
	catalog := getReply(pathModels, 200, catalogBody)
	return []snapshotCase{
		{
			name:        "catalog_models_for_auth",
			description: "model.static, model.for_auth loading the catalog (with cost) on first use and reusing it, a model selection with a retired model, and the management models view",
			credentials: []caseCredential{k1, k2},
			script:      []snapshotReply{catalog},
			steps: []caseStep{
				bare(pluginabi.MethodModelStatic),
				modelsForAuthStep(k1, false),
				modelsForAuthStep(k2, true),
				mgmt(http.MethodGet, mgmtBase+"/models", ""),
			},
		},
		{
			name:        "catalog_refresh_on_register",
			description: "The refresh plugin.register queues re-saves auth files whose model_revision is stale; a forced refresh with the same catalog saves nothing; a new model-prefix re-registers enabled credentials",
			credentials: []caseCredential{k1, disabled},
			script:      []snapshotReply{catalog, catalog, catalog},
			steps: []caseStep{
				lifecycleStep(pluginabi.MethodPluginReconfigure, pluginabi.SchemaVersion, snapshotConfig(nil)),
				mgmt(http.MethodPost, mgmtBase+"/models/refresh", ""),
				lifecycleStep(pluginabi.MethodPluginReconfigure, pluginabi.SchemaVersion, snapshotConfig(map[string]any{"model-prefix": "oc"})),
				mgmt(http.MethodGet, mgmtBase+"/status", ""),
			},
		},
		{
			name:        "catalog_fetch_error",
			description: "Catalog failures: model.for_auth registers nothing for an all-models key, the models view answers 502 and the status shows the error",
			credentials: []caseCredential{k1},
			script: []snapshotReply{
				reply(http.MethodGet, pathModels, 500, "text/plain", "upstream down"),
				getReply(pathModels, 200, `{"object":"list","data":[]}`),
			},
			steps: []caseStep{
				modelsForAuthStep(k1, false),
				mgmt(http.MethodGet, mgmtBase+"/models", ""),
				mgmt(http.MethodGet, mgmtBase+"/status", ""),
			},
		},
	}
}

func credentialCases() []snapshotCase {
	usageOK := getReply(pathGoUsage, 200, goUsageBody)
	catalog := getReply(pathModels, 200, catalogBody)
	k1 := opencodeCred(1, "opencode-k1.json", map[string]any{"api_key": fakeKey1, "label": "Primary", "models": []string{"kimi-k3"}, "priority": 2})
	status1 := opencodeCred(1, "opencode-k1.json", map[string]any{"api_key": fakeKey1, "label": "Primary", "note": "main key", "priority": 3, "prefix": "oc", "models": []string{"kimi-k3", "retired-model"}})
	status2 := opencodeCred(2, "opencode-k2.json", map[string]any{"api_key": fakeKey2, "disabled": true, "base_url": "https://example.invalid/inference", "proxy_url": "http://user:secret@proxy.invalid:8080"})
	codex := caseCredential{name: "codex-a.json", index: "idx-3", fields: map[string]any{"type": "codex", "access_token": "fake-access-token"}}
	status4 := opencodeCred(4, "opencode-k4.json", map[string]any{"label": "No key"})
	parseRequest := func(name, raw string) caseStep {
		return caseStep{method: pluginabi.MethodAuthParse, request: pluginapi.AuthParseRequest{Provider: "opencode", Path: snapshotAuthDir + "/" + name, FileName: name, RawJSON: []byte(raw), Host: pluginapi.HostConfigSummary{AuthDir: snapshotAuthDir}}}
	}
	refreshRequest := func(storage string, metadata map[string]any) caseStep {
		return caseStep{method: pluginabi.MethodAuthRefresh, request: cpaAuthRefreshRequest{AuthRefreshRequest: pluginapi.AuthRefreshRequest{
			AuthID: "opencode-k1.json", AuthProvider: "opencode", StorageJSON: []byte(storage), Metadata: metadata, Host: pluginapi.HostConfigSummary{AuthDir: snapshotAuthDir},
		}, HostCallbackID: hostCallbackID}}
	}
	return []snapshotCase{
		{
			name:        "credential_add",
			description: "Adding keys through the management API: key check against Go usage, catalog load, save through the host, duplicate and invalid input",
			script:      []snapshotReply{usageOK, catalog},
			steps: []caseStep{
				mgmt(http.MethodPost, mgmtBase+"/credentials", `{"api_key":"`+fakeKey1+`","label":"Primary","note":"added by snapshot","models":["kimi-k3"," glm-5","kimi-k3","retired-model"],"prefix":"/oc/"}`),
				mgmt(http.MethodPost, mgmtBase+"/credentials", `{"api_key":"`+fakeKey1+`"}`),
				mgmt(http.MethodPost, mgmtBase+"/credentials", `{"api_key":"oc_sk has space"}`),
				mgmt(http.MethodPost, mgmtBase+"/credentials", `{"api_key":"`+fakeKey2+`","skip_verify":true,"proxy_url":"direct","priority":7}`),
				mgmt(http.MethodPost, mgmtBase+"/credentials", ""),
				mgmt(http.MethodPost, mgmtBase+"/credentials", `{"api_key":"`+fakeKey3+`","skip_verify":true,"base_url":"ftp://example.invalid"}`),
			},
		},
		{
			name:        "credential_add_key_check",
			description: "The key check accepts 403 (no Go plan) and rejects 401 (400 to the page) and 500/429 (502)",
			script: []snapshotReply{
				getReply(pathGoUsage, 403, `{"type":"error","error":{"type":"ForbiddenError","message":"Workspace has no Go subscription"}}`),
				catalog,
				getReply(pathGoUsage, 401, `{"type":"error","error":{"type":"AuthError","message":"Invalid API key."}}`),
				getReply(pathGoUsage, 500, `{"error":"internal"}`),
				withReplyHeader(getReply(pathGoUsage, 429, `{"error":{"message":"slow down"}}`), "Retry-After", "5"),
			},
			steps: []caseStep{
				mgmt(http.MethodPost, mgmtBase+"/credentials", `{"api_key":"`+fakeKey1+`"}`),
				mgmt(http.MethodPost, mgmtBase+"/credentials", `{"api_key":"`+fakeKey2+`"}`),
				mgmt(http.MethodPost, mgmtBase+"/credentials", `{"api_key":"`+fakeKey3+`"}`),
				mgmt(http.MethodPost, mgmtBase+"/credentials", `{"api_key":"`+fakeKey4+`"}`),
			},
		},
		{
			name:        "credential_update",
			description: "Editing a credential through the management API (fields, models, prefix, base_url) and the rejected edits",
			credentials: []caseCredential{k1},
			script:      []snapshotReply{catalog},
			steps: []caseStep{
				mgmt(http.MethodPatch, mgmtBase+"/credentials", `{"auth_index":"idx-1","label":"Renamed","note":"edited","models":null,"priority":0,"proxy_url":"direct","base_url":"{{UPSTREAM}}/inference/go/openai/v1/"}`),
				mgmt(http.MethodPatch, mgmtBase+"/credentials", `{"name":"opencode-k1.json","api_key":"oc_sk_new"}`),
				mgmt(http.MethodPatch, mgmtBase+"/credentials", `{"auth_index":"idx-9","label":"x"}`),
				mgmt(http.MethodPatch, mgmtBase+"/credentials", `{"auth_index":"idx-1","prefix":"has space"}`),
				mgmt(http.MethodPatch, mgmtBase+"/credentials", `{"auth_index":"idx-1","proxy_id":"missing"}`),
				mgmt(http.MethodPatch, mgmtBase+"/credentials", `{"name":"opencode-k1.json","models":["glm-5","unknown-x"],"prefix":null,"priority":4,"base_url":""}`),
				mgmt(http.MethodPut, mgmtBase+"/credentials", `{}`),
			},
		},
		{
			name:        "credential_status_view",
			description: "Credential views in the status: labels, notes, priority, prefixes, model selection, disabled, redacted proxy, other providers skipped, missing key; missing models once the catalog is loaded",
			credentials: []caseCredential{status1, status2, codex, status4},
			script:      []snapshotReply{catalog},
			steps: []caseStep{
				mgmt(http.MethodGet, mgmtBase+"/status", ""),
				modelsForAuthStep(status1, false),
				mgmt(http.MethodGet, mgmtBase+"/status/", ""),
			},
		},
		{
			name:        "credential_auth_parse_refresh",
			description: "auth.parse and auth.refresh of OpenCode auth files, files of other providers, files without a key, and the login RPCs",
			steps: []caseStep{
				parseRequest("opencode-k1.json", `{"type":"opencode","api_key":"`+fakeKey1+`","proxy_url":"direct"}`),
				parseRequest("opencode-k2.json", `{"type":"OpenCode","api_key":"`+fakeKey2+`","label":"Second","prefix":"/team/","disabled":true,"priority":5}`),
				parseRequest("codex-a.json", `{"type":"codex","access_token":"fake"}`),
				parseRequest("opencode-nokey.json", `{"type":"opencode"}`),
				parseRequest("broken.json", `not json`),
				refreshRequest(`{"type":"opencode","api_key":"`+fakeKey1+`"}`, map[string]any{"label": "From metadata", "type": "opencode"}),
				refreshRequest(`{"type":"opencode"}`, nil),
				bare(pluginabi.MethodAuthLoginStart),
				bare(pluginabi.MethodAuthLoginPoll),
			},
		},
	}
}

func quotaCases() []snapshotCase {
	k := func(index int, key string) caseCredential {
		return opencodeCred(index, fmt.Sprintf("opencode-k%d.json", index), map[string]any{"api_key": key})
	}
	k1, k2, k3, k4 := k(1, fakeKey1), k(2, fakeKey2), k(3, fakeKey3), k(4, fakeKey4)
	noKey := opencodeCred(4, "opencode-nokey.json", map[string]any{})
	quotaPath := mgmtBase + "/credentials/quota"
	byIndex := func(index string) caseStep {
		return mgmtQuery(http.MethodGet, quotaPath, url.Values{"auth_index": {index}})
	}
	return []snapshotCase{
		{
			name:        "quota_fetch_go_usage",
			description: "quota.fetch reads Go usage from the console origin (5h/7d/monthly, rate-limited window as 0); repeats come from the cache, also when CPA sends only the auth index",
			credentials: []caseCredential{k1},
			script:      []snapshotReply{getReply(pathGoUsage, 200, goUsageBody)},
			steps: []caseStep{
				bare(pluginabi.MethodQuotaDescribe),
				quotaStep(k1, true),
				quotaStep(k1, true),
				quotaStep(k1, false),
			},
		},
		{
			name:        "quota_fetch_no_go_plan",
			description: "A 403 from Go usage means no Go plan: an empty quota, cached",
			credentials: []caseCredential{k1},
			script:      []snapshotReply{getReply(pathGoUsage, 403, `{"type":"error","error":{"type":"ForbiddenError","message":"Workspace has no Go subscription"}}`)},
			steps:       []caseStep{quotaStep(k1, true), quotaStep(k1, true)},
		},
		{
			name:        "quota_fetch_errors",
			description: "Go usage errors are returned and not cached: 401, 500 plain text, 429 with Retry-After, a non-JSON 200, and a credential without a key",
			credentials: []caseCredential{k1, k2, k3, noKey},
			script: []snapshotReply{
				getReply(pathGoUsage, 401, `{"type":"error","error":{"type":"AuthError","message":"Invalid API key."}}`),
				reply(http.MethodGet, pathGoUsage, 500, "text/plain", "boom"),
				withReplyHeader(getReply(pathGoUsage, 429, `{"error":{"message":"Too many requests"}}`), "Retry-After", "12"),
				reply(http.MethodGet, pathGoUsage, 200, "text/html", "<html>maintenance</html>"),
			},
			steps: []caseStep{
				quotaStep(k1, true),
				quotaStep(k2, true),
				quotaStep(k3, true),
				quotaStep(k1, true),
				quotaStep(noKey, false),
			},
		},
		{
			name:        "quota_management_view",
			description: "The management quota view: Go usage, no Go plan, rejected key (400), upstream failure (502), unknown credential (404), cached repeat",
			credentials: []caseCredential{k1, k2, k3, k4},
			script: []snapshotReply{
				getReply(pathGoUsage, 200, goUsageBody),
				getReply(pathGoUsage, 403, `{"type":"error","error":{"type":"ForbiddenError","message":"Workspace has no Go subscription"}}`),
				getReply(pathGoUsage, 401, `{"type":"error","error":{"type":"AuthError","message":"Invalid API key."}}`),
				reply(http.MethodGet, pathGoUsage, 503, "text/plain", "unavailable"),
			},
			steps: []caseStep{
				byIndex("idx-1"),
				byIndex("idx-2"),
				byIndex("idx-3"),
				byIndex("idx-4"),
				byIndex("idx-99"),
				byIndex("idx-1"),
			},
		},
	}
}

func managementCases(k1 caseCredential) []snapshotCase {
	intercept := func(trace string) caseStep {
		return caseStep{method: pluginabi.MethodRequestInterceptAfter, request: cpaRequestInterceptRequest{RequestInterceptRequest: pluginapi.RequestInterceptRequest{
			RequestID:      "exec-1",
			TraceID:        trace,
			SourceFormat:   "openai",
			ToFormat:       "openai",
			Model:          "kimi-k3",
			RequestedModel: "opencode/kimi-k3",
			Headers:        http.Header{"Content-Type": {"application/json"}},
			Metadata:       map[string]any{"selected_auth_id": k1.name},
		}, HostCallbackID: hostCallbackID}}
	}
	traced := withHeader("X-OpenCode-Cpa-Request-Id", "trace-0001")
	logsPath := mgmtBase + "/logs"
	proxiesPath := mgmtBase + "/proxies"
	p1 := opencodeCred(1, "opencode-p1.json", map[string]any{"api_key": fakeKey1, "proxy_url": "socks5://user:pw@10.0.0.1:1080"})
	p2 := opencodeCred(2, "opencode-p2.json", map[string]any{"api_key": fakeKey2, "proxy_url": "direct"})
	p3 := opencodeCred(3, "opencode-p3.json", map[string]any{"api_key": fakeKey3, "proxy_url": "http://10.0.0.2:3128"})
	return []snapshotCase{
		{
			name:        "management_status",
			description: "Management routes, the status view, logo and flag resources, unknown routes and methods, the before-auth interceptor",
			credentials: []caseCredential{k1},
			steps: []caseStep{
				bare(pluginabi.MethodManagementRegister),
				mgmt(http.MethodGet, mgmtBase+"/status", ""),
				mgmt(http.MethodGet, "/v0/resource/plugins/opencode/logo.svg", ""),
				mgmtQuery(http.MethodGet, "/v0/resource/plugins/opencode/flag.svg", url.Values{"cc": {"jp"}}),
				mgmtQuery(http.MethodGet, "/v0/resource/plugins/opencode/flag.svg", url.Values{"cc": {"zz"}}),
				mgmt(http.MethodGet, mgmtBase+"/nope", ""),
				mgmt(http.MethodDelete, mgmtBase+"/status", ""),
				{method: pluginabi.MethodRequestInterceptBefore, request: cpaRequestInterceptRequest{RequestInterceptRequest: pluginapi.RequestInterceptRequest{RequestID: "exec-0", Model: "opencode/kimi-k3"}, HostCallbackID: hostCallbackID}},
			},
		},
		{
			name:        "management_logs",
			description: "The after-auth interceptor stamps CPA's request ID on OpenCode requests; request log rows of a success, an error and a stream; filters, clearing",
			credentials: []caseCredential{k1},
			script: []snapshotReply{
				withReplyHeader(jsonReply(pathChat, 200, chatBody), "X-Request-Id", "oc-req-log-1"),
				jsonReply(pathChat, 500, `{"error":{"message":"internal server error"}}`),
				sseReply(pathMessages, msgStreamEvents...),
			},
			steps: []caseStep{
				intercept("trace-0001"),
				execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "openai", chatPayload)),
				intercept("trace-0001"),
				intercept(""),
				execStep(pluginabi.MethodExecutorExecute, execRequest(k1, "openai", chatPayload, traced)),
				execStep(pluginabi.MethodExecutorExecuteStream, execRequest(k1, "claude", msgStreamPayload, streaming("stream-log-1"), traced, withHeader("User-Agent", "claude-cli/2.1.0 (external, cli)"))),
				mgmt(http.MethodGet, logsPath, ""),
				mgmtQuery(http.MethodGet, logsPath, url.Values{"status": {"error"}, "limit": {"10"}}),
				mgmtQuery(http.MethodGet, logsPath, url.Values{"request_id": {"trace-0001"}}),
				mgmtQuery(http.MethodGet, logsPath, url.Values{"search": {"claude-cli"}, "offset": {"0"}}),
				mgmtQuery(http.MethodGet, logsPath, url.Values{"from": {"yesterday"}}),
				mgmt(http.MethodDelete, logsPath, ""),
				mgmt(http.MethodGet, logsPath, ""),
			},
		},
		{
			name:        "management_proxies",
			description: "The proxy list: add (redacted URL), invalid and duplicate URLs, import from credentials, status matching, unknown IDs",
			credentials: []caseCredential{p1, p2, p3},
			steps: []caseStep{
				mgmt(http.MethodGet, proxiesPath, ""),
				mgmt(http.MethodPost, proxiesPath, `{"name":"Tokyo","url":"http://user:pass@10.0.0.3:3128"}`),
				mgmt(http.MethodPost, proxiesPath, `{"name":"Bad","url":"ftp://10.0.0.4:21"}`),
				mgmt(http.MethodPost, proxiesPath, `{"url":"http://user:pass@10.0.0.3:3128"}`),
				mgmt(http.MethodPost, proxiesPath+"/import", ""),
				mgmt(http.MethodGet, proxiesPath, ""),
				mgmt(http.MethodGet, mgmtBase+"/status", ""),
				mgmtQuery(http.MethodDelete, proxiesPath, url.Values{"id": {"missing"}}),
				mgmt(http.MethodPatch, proxiesPath, `{"id":"missing","name":"x"}`),
				mgmt(http.MethodPost, proxiesPath+"/check", `{"target":"nowhere"}`),
				mgmt(http.MethodPost, proxiesPath+"/order", `{"ids":["missing"]}`),
				mgmt(http.MethodPost, mgmtBase+"/credentials", `{"api_key":"`+fakeKey4+`","proxy_id":"missing"}`),
			},
		},
	}
}

func lifecycleCases() []snapshotCase {
	catalog := getReply(pathModels, 200, catalogBody)
	return []snapshotCase{
		{
			name:        "lifecycle_register_and_identifiers",
			description: "plugin.register (schema 10 and a host without a schema), config warnings, identifiers, unsupported operations, unknown methods, shutdown",
			script:      []snapshotReply{catalog, reply(http.MethodGet, pathModels, 503, "text/plain", "maintenance")},
			steps: []caseStep{
				lifecycleStep(pluginabi.MethodPluginRegister, pluginabi.SchemaVersion, snapshotConfig(nil)),
				lifecycleStep(pluginabi.MethodPluginReconfigure, 0, snapshotConfig(map[string]any{"model-prefix": "/oc/", "request-timeout-seconds": -5, "stream-idle-timeout-seconds": 0, "models-refresh-minutes": -1, "log-retention": -1})),
				bare(pluginabi.MethodPluginQuiesce),
				bare(pluginabi.MethodAuthIdentifier),
				bare(pluginabi.MethodExecutorIdentifier),
				bare(pluginabi.MethodQuotaIdentifier),
				{method: pluginabi.MethodExecutorHTTPRequest, request: map[string]any{"Method": "GET", "URL": "https://opencode.ai/"}},
				bare(pluginabi.MethodQuotaReset),
				bare("model.register"),
				bare(pluginabi.MethodPluginShutdown),
			},
		},
	}
}

// snapshotYAML renders a plugin config object as the YAML CPA passes.
func snapshotYAML(config map[string]any) ([]byte, error) {
	return yaml.Marshal(config)
}
