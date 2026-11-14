package plugin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// The fake CPA host and the scripted upstream used by the snapshot tests.
// Adapted from cpa-wokey-plugin's fakehost_test.go: the host goes through
// the plugin's RPCs (auth.parse, model.for_auth) instead of calling its
// internals, and every callback is recorded for the snapshot.

// fakeAuthDir is the auth directory the fake host reports.
const fakeAuthDir = "/auths"

// fakeHostSummary is the host config summary the fake host sends with
// auth.parse and model.for_auth.
var fakeHostSummary = pluginapi.HostConfigSummary{AuthDir: fakeAuthDir}

// cpaAuthModelRequest mirrors CPA's rpcAuthModelRequest.
type cpaAuthModelRequest struct {
	pluginapi.AuthModelRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// fakeAuthFile is one auth file held by fakeHost.
type fakeAuthFile struct {
	index    string
	name     string
	provider string
	label    string
	raw      []byte
	disabled bool
	// models are the IDs CPA registered for the file (with its prefix
	// rules); nil until the file was saved through host.auth.save.
	models []string
}

// hostCallRecord is one callback the plugin made.
type hostCallRecord struct {
	method  string
	request []byte
}

// fakeHost is an in-memory stand-in for the CPA host callbacks. Saving a
// file makes CPA parse it with auth.parse and register its models with
// model.for_auth, as CPA does.
type fakeHost struct {
	svc *Service

	mu        sync.Mutex
	files     []*fakeAuthFile
	nextIndex int
	calls     []hostCallRecord
}

func newFakeHost() *fakeHost {
	return &fakeHost{}
}

func okResult(v any) []byte {
	return okEnvelope(v)
}

func failResult(message string) []byte {
	return ErrorEnvelope("host_error", message)
}

// takeCalls returns the callbacks recorded since the last call.
func (h *fakeHost) takeCalls() []hostCallRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	calls := h.calls
	h.calls = nil
	return calls
}

func (h *fakeHost) call(method string, request []byte) ([]byte, error) {
	h.mu.Lock()
	h.calls = append(h.calls, hostCallRecord{method: method, request: append([]byte(nil), request...)})
	h.mu.Unlock()
	switch method {
	case pluginabi.MethodHostLog, pluginabi.MethodHostStreamEmit, pluginabi.MethodHostStreamClose:
		return okResult(struct{}{}), nil
	case pluginabi.MethodHostAuthList:
		h.mu.Lock()
		defer h.mu.Unlock()
		files := make([]pluginapi.HostAuthFileEntry, 0, len(h.files))
		for _, file := range h.files {
			files = append(files, pluginapi.HostAuthFileEntry{
				ID:        file.name,
				AuthIndex: file.index,
				Name:      file.name,
				Type:      file.provider,
				Provider:  file.provider,
				Label:     file.label,
				Disabled:  file.disabled,
				Source:    "file",
				Path:      path.Join(fakeAuthDir, file.name),
			})
		}
		return okResult(rpcHostAuthListResponse{Files: files}), nil
	case pluginabi.MethodHostAuthGet:
		var req pluginapi.HostAuthGetRequest
		if errUnmarshal := json.Unmarshal(request, &req); errUnmarshal != nil {
			return failResult(errUnmarshal.Error()), nil
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, file := range h.files {
			if file.index == req.AuthIndex {
				return okResult(pluginapi.HostAuthGetResponse{AuthIndex: file.index, Name: file.name, Path: path.Join(fakeAuthDir, file.name), JSON: file.raw}), nil
			}
		}
		return failResult("auth not found"), nil
	case pluginabi.MethodHostAuthSave:
		var req pluginapi.HostAuthSaveRequest
		if errUnmarshal := json.Unmarshal(request, &req); errUnmarshal != nil {
			return failResult(errUnmarshal.Error()), nil
		}
		return h.save(req), nil
	case pluginabi.MethodHostAuthModels:
		h.mu.Lock()
		defer h.mu.Unlock()
		out := pluginapi.HostAuthModelsResponse{Credentials: []pluginapi.HostAuthModels{}}
		for _, file := range h.files {
			if file.disabled {
				continue
			}
			out.Credentials = append(out.Credentials, pluginapi.HostAuthModels{ID: file.name, AuthIndex: file.index, Provider: file.provider, Models: append([]string{}, file.models...)})
		}
		return okResult(out), nil
	default:
		return failResult("unexpected host method " + method), nil
	}
}

// save mimics host.auth.save: CPA parses the file through the plugin, then
// asks it for the credential's models. The lock is released while calling
// into the plugin, which may call back into the host.
func (h *fakeHost) save(req pluginapi.HostAuthSaveRequest) []byte {
	name := path.Base(req.Name)
	if !strings.HasSuffix(strings.ToLower(name), ".json") {
		return failResult("auth file name must end with .json")
	}
	raw := append([]byte(nil), req.JSON...)
	parsed, errParse := h.parse(name, raw)
	if errParse != "" {
		return failResult(errParse)
	}
	if !parsed.Handled {
		return failResult("no provider handled the auth file")
	}
	var models []string
	if !parsed.Auth.Disabled {
		models = h.register(name, raw, parsed.Auth)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	file := h.fileLocked(name)
	if file == nil {
		h.nextIndex++
		file = &fakeAuthFile{index: fmt.Sprintf("idx-%d", h.nextIndex), name: name}
		h.files = append(h.files, file)
	}
	file.provider = parsed.Auth.Provider
	file.label = parsed.Auth.Label
	file.raw = raw
	file.disabled = parsed.Auth.Disabled
	file.models = models
	return okResult(pluginapi.HostAuthSaveResponse{Name: name, Path: path.Join(fakeAuthDir, name)})
}

// parse runs auth.parse for a file.
func (h *fakeHost) parse(name string, raw []byte) (pluginapi.AuthParseResponse, string) {
	request, _ := json.Marshal(pluginapi.AuthParseRequest{Provider: ProviderID, Path: path.Join(fakeAuthDir, name), FileName: name, RawJSON: raw, Host: fakeHostSummary})
	var out pluginapi.AuthParseResponse
	if errCall := decodeRPCResult(h.svc.Call(pluginabi.MethodAuthParse, request), &out); errCall != "" {
		return out, errCall
	}
	return out, ""
}

// register runs model.for_auth for a parsed file and returns the model IDs
// CPA registers for it.
func (h *fakeHost) register(name string, raw []byte, auth pluginapi.AuthData) []string {
	request, _ := json.Marshal(cpaAuthModelRequest{AuthModelRequest: pluginapi.AuthModelRequest{
		AuthID:       name,
		AuthProvider: auth.Provider,
		StorageJSON:  raw,
		Metadata:     auth.Metadata,
		Host:         fakeHostSummary,
	}})
	var resp pluginapi.ModelResponse
	if errCall := decodeRPCResult(h.svc.Call(pluginabi.MethodModelForAuth, request), &resp); errCall != "" {
		return nil
	}
	return cpaPrefixedIDs(resp.Models, cpaAuthPrefix(auth), fakeHostSummary.ForceModelPrefix)
}

func decodeRPCResult(raw []byte, out any) string {
	var envelope pluginabi.Envelope
	if errUnmarshal := json.Unmarshal(raw, &envelope); errUnmarshal != nil {
		return errUnmarshal.Error()
	}
	if !envelope.OK {
		if envelope.Error != nil {
			return envelope.Error.Message
		}
		return "plugin call failed"
	}
	if errResult := json.Unmarshal(envelope.Result, out); errResult != nil {
		return errResult.Error()
	}
	return ""
}

func (h *fakeHost) fileLocked(name string) *fakeAuthFile {
	for _, file := range h.files {
		if file.name == name {
			return file
		}
	}
	return nil
}

// cpaAuthPrefix mirrors how CPA resolves an auth's prefix: the plugin's
// AuthData.Prefix, else the file's own prefix field.
func cpaAuthPrefix(auth pluginapi.AuthData) string {
	if prefix := strings.TrimSpace(auth.Prefix); prefix != "" {
		return prefix
	}
	if prefix, ok := auth.Metadata["prefix"].(string); ok {
		return strings.Trim(strings.TrimSpace(prefix), "/")
	}
	return ""
}

// cpaPrefixedIDs mirrors CPA's applyModelPrefixes: without
// force-model-prefix both the bare and the prefixed ID are registered.
func cpaPrefixedIDs(models []pluginapi.ModelInfo, prefix string, force bool) []string {
	out := []string{}
	for _, model := range models {
		if prefix == "" {
			out = append(out, model.ID)
			continue
		}
		if !force {
			out = append(out, model.ID)
		}
		out = append(out, prefix+"/"+model.ID)
	}
	return out
}

// seed stores an auth file directly, as if CPA had loaded it from the auth
// directory at startup. Its provider and disabled flag come from the file;
// no models are registered for it until it is saved.
func (h *fakeHost) seed(name string, raw []byte) {
	var fields struct {
		Type     string `json:"type"`
		Label    string `json:"label"`
		Disabled bool   `json:"disabled"`
	}
	_ = json.Unmarshal(raw, &fields)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextIndex++
	h.files = append(h.files, &fakeAuthFile{
		index:    fmt.Sprintf("idx-%d", h.nextIndex),
		name:     name,
		provider: fields.Type,
		label:    fields.Label,
		raw:      append([]byte(nil), raw...),
		disabled: fields.Disabled,
	})
}

// upstreamReply is one scripted upstream response.
type upstreamReply struct {
	Method string      `json:"method"`
	Path   string      `json:"path"`
	Status int         `json:"status"`
	Header http.Header `json:"header"`
	Body   string      `json:"body"`
	Chunks []string    `json:"chunks"`
}

// upstreamRecord is one request the scripted upstream received.
type upstreamRecord struct {
	Method string      `json:"method"`
	Path   string      `json:"path"`
	Query  string      `json:"query"`
	Header http.Header `json:"header"`
	Body   string      `json:"body"`
}

// scriptedUpstream answers each request with the next scripted reply for
// its method and path and records what it received.
type scriptedUpstream struct {
	*httptest.Server

	mu        sync.Mutex
	queues    map[string][]upstreamReply
	requests  []upstreamRecord
	unmatched []string
}

func newScriptedUpstream(script []upstreamReply) *scriptedUpstream {
	u := &scriptedUpstream{queues: map[string][]upstreamReply{}}
	for _, reply := range script {
		key := reply.Method + " " + reply.Path
		u.queues[key] = append(u.queues[key], reply)
	}
	u.Server = httptest.NewServer(http.HandlerFunc(u.serve))
	return u
}

func (u *scriptedUpstream) serve(rw http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	key := r.Method + " " + r.URL.Path
	u.mu.Lock()
	u.requests = append(u.requests, upstreamRecord{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: string(body)})
	queue := u.queues[key]
	var reply upstreamReply
	matched := len(queue) > 0
	if matched {
		reply, u.queues[key] = queue[0], queue[1:]
	} else {
		u.unmatched = append(u.unmatched, key)
	}
	u.mu.Unlock()
	if !matched {
		http.Error(rw, "snapshot: unscripted upstream request "+key, http.StatusTeapot)
		return
	}
	for name, values := range reply.Header {
		rw.Header()[name] = append([]string(nil), values...)
	}
	status := reply.Status
	if status == 0 {
		status = http.StatusOK
	}
	if len(reply.Chunks) == 0 {
		rw.WriteHeader(status)
		_, _ = io.WriteString(rw, reply.Body)
		return
	}
	flusher, _ := rw.(http.Flusher)
	rw.WriteHeader(status)
	for _, chunk := range reply.Chunks {
		_, _ = io.WriteString(rw, chunk)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// take returns the requests received since the last call and the
// unscripted ones among them.
func (u *scriptedUpstream) take() ([]upstreamRecord, []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	requests, unmatched := u.requests, u.unmatched
	u.requests, u.unmatched = nil, nil
	return requests, unmatched
}

// leftover lists the scripted replies nobody asked for.
func (u *scriptedUpstream) leftover() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []string
	for key, queue := range u.queues {
		for range queue {
			out = append(out, key)
		}
	}
	return out
}
