package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/berry-shake/cpa-opencode-plugin/internal/credential"
	"github.com/berry-shake/cpa-opencode-plugin/internal/opencode"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const (
	defaultAnthropicVersion = "2023-06-01"

	// maxResponseBytes bounds a buffered (non-streaming) upstream response.
	maxResponseBytes = 64 << 20
)

// executorFormats are the protocols OpenCode serves: OpenAI chat completions
// and Responses under /openai/v1 and Anthropic Messages under /anthropic/v1.
// Every text model answers on all three (Console translates for models whose
// native API is another one), so declaring them as both input and output
// lets CPA pass requests through untranslated.
var executorFormats = []string{"chat-completions", "responses", "anthropic"}

// forwardedRequestHeaders are the client headers that change upstream
// behavior and are safe to pass on. Client credentials are never forwarded;
// the key goes out as a bearer token. The session header is set separately
// (see sessionID).
var forwardedRequestHeaders = []string{"Anthropic-Beta", "Anthropic-Version"}

// forwardedResponseHeaders are the upstream headers returned to CPA.
var forwardedResponseHeaders = []string{"Content-Type", "X-Request-Id", "Request-Id", "Retry-After"}

// upstreamCall is a fully prepared OpenCode request.
type upstreamCall struct {
	endpoint string
	url      string
	body     []byte
	header   http.Header
	client   *http.Client
	authID   string
	// model, upstreamModel, effort, keyMasked and egress describe the call
	// for the request log.
	model         string
	upstreamModel string
	effort        string
	keyMasked     string
	egress        string
}

// requestMembers are the top-level request members prepareCall reads or
// edits; the rest of the body is passed through without being decoded.
var requestMembers = append([]string{"model", "stream_options", "reasoning_effort", "reasoning", "output_config", "thinking"}, sessionMembers...)

// prepareCall resolves the credential, egress and upstream model for one
// executor request.
func (s *Service) prepareCall(req pluginapi.ExecutorRequest, endpoint string) (*upstreamCall, *pluginabi.Error) {
	s.noteOpenCodeAuth(req.AuthID)
	cred, _, errCred := credential.FromMaps(req.StorageJSON, req.AuthMetadata)
	if errCred != nil {
		return nil, opencode.AuthError("opencode: " + errCred.Error())
	}
	if cred.APIKey == "" {
		return nil, opencode.AuthError("opencode: credential has no api_key")
	}
	cfg := s.config()
	members, errScan := topLevelMembers(req.Payload, requestMembers...)
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = memberString(req.Payload, members, "model")
	}
	if model == "" {
		return nil, opencode.RequestError("invalid_request", "opencode: request has no model")
	}
	upstream := s.catalog.Current().UpstreamID(model, cred.EffectivePrefix(cfg))
	encodedUpstream, _ := json.Marshal(upstream)
	body, errBody := setMember(req.Payload, members, errScan, "model", encodedUpstream)
	if errBody != nil {
		return nil, opencode.RequestError("invalid_request", "opencode: "+errBody.Error())
	}
	if _, present := members["stream_options"]; endpoint == opencode.EndpointChat && req.Stream && !present && errScan == nil {
		// Ask for the final usage chunk so CPA can account the request.
		body = insertTopLevelMember(body, "stream_options", []byte(`{"include_usage":true}`))
	}
	effort := ""
	if errScan == nil {
		effort = reasoningEffort(req.Payload, members)
	}
	setting, errProxy := opencode.ResolveEgress(cred.ProxyURL, s.globalProxy())
	if errProxy != nil {
		return nil, &pluginabi.Error{Code: "invalid_proxy", Message: "opencode: " + errProxy.Error(), HTTPStatus: http.StatusBadGateway}
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+cred.APIKey)
	header.Set("Content-Type", "application/json")
	header.Set("User-Agent", s.userAgent())
	if req.Stream {
		header.Set("Accept", "text/event-stream")
	} else {
		header.Set("Accept", "application/json")
	}
	for _, name := range forwardedRequestHeaders {
		if value := strings.TrimSpace(req.Headers.Get(name)); value != "" {
			header.Set(name, value)
		}
	}
	session := ""
	if errScan == nil {
		session = sessionID(req.Headers, req.Payload, members)
	}
	if session == "" {
		session = "cpa_" + strings.ReplaceAll(strings.TrimSpace(req.AuthID), " ", "_")
	}
	header.Set(sessionHeader, session)
	if endpoint == opencode.EndpointMessages || endpoint == opencode.EndpointCountTokens {
		if header.Get("Anthropic-Version") == "" {
			header.Set("Anthropic-Version", defaultAnthropicVersion)
		}
	} else {
		header.Del("Anthropic-Beta")
		header.Del("Anthropic-Version")
	}
	return &upstreamCall{
		endpoint: endpoint,
		url:      cred.EffectiveBaseURL(cfg) + endpoint,
		body:     body,
		header:   header,
		client:   s.clients.Client(setting),
		authID:   req.AuthID,

		model:         clientModel(req, model),
		upstreamModel: upstream,
		effort:        effort,
		keyMasked:     credential.MaskKey(cred.APIKey),
		egress:        setting.Label(),
	}, nil
}

func (c *upstreamCall) newRequest(ctx context.Context) (*http.Request, error) {
	httpReq, errReq := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(c.body))
	if errReq != nil {
		return nil, errReq
	}
	httpReq.Header = c.header.Clone()
	return httpReq, nil
}

// execute performs a buffered request and returns the upstream body as is.
func (s *Service) execute(req pluginapi.ExecutorRequest) []byte {
	endpoint, ok := opencode.EndpointForFormat(req.Format)
	if !ok {
		return errorEnvelopeFrom(opencode.RequestError("unsupported_format", "opencode: unsupported executor format "+req.Format))
	}
	return s.executeBuffered(req, endpoint)
}

// countTokens forwards an Anthropic count_tokens request. It is not written
// to the request log: it generates nothing and costs nothing.
func (s *Service) countTokens(req pluginapi.ExecutorRequest) []byte {
	return s.executeBuffered(req, opencode.EndpointCountTokens)
}

func (s *Service) executeBuffered(req pluginapi.ExecutorRequest, endpoint string) []byte {
	call, perr := s.prepareCall(req, endpoint)
	if perr != nil {
		if endpoint != opencode.EndpointCountTokens {
			s.recordFailure(req, endpoint, false, perr)
		}
		return errorEnvelopeFrom(perr)
	}
	var rec *callRecord
	if endpoint != opencode.EndpointCountTokens {
		rec = s.startRecord(req, call, false)
	}
	fail := func(perr *pluginabi.Error) []byte {
		rec.finish(perr, "request")
		return errorEnvelopeFrom(perr)
	}
	ctx, cancel := context.WithTimeout(s.ctx, s.config().RequestTimeout)
	defer cancel()
	httpReq, errReq := call.newRequest(ctx)
	if errReq != nil {
		return fail(opencode.TransportError(errReq))
	}
	resp, errDo := call.client.Do(httpReq)
	if errDo != nil {
		return fail(opencode.TransportError(errDo))
	}
	defer func() { _ = resp.Body.Close() }()
	rec.response(resp)
	body, errRead := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if errRead != nil {
		return fail(opencode.TransportError(fmt.Errorf("read response: %w", errRead)))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fail(opencode.UpstreamError(resp.StatusCode, resp.Header, body, endpoint))
	}
	rec.observeJSON(body)
	rec.finish(nil, "")
	return okEnvelope(pluginapi.ExecutorResponse{
		Payload: body,
		Headers: filterHeaders(resp.Header, forwardedResponseHeaders),
	})
}

func filterHeaders(source http.Header, names []string) http.Header {
	out := http.Header{}
	for _, name := range names {
		if values := source.Values(name); len(values) > 0 {
			out[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
		}
	}
	return out
}
