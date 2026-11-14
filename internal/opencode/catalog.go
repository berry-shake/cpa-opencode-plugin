package opencode

// The catalog caches OpenCode's model list and turns it into CPA model
// registrations.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/berry-shake/cpa-provider-core/provider"
	"github.com/berry-shake/cpa-provider-core/upstream"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// maxCatalogBytes bounds the /v1/models response (the Go list is ~36 KB, the
// pay-as-you-go one ~100 KB).
const maxCatalogBytes = 16 << 20

// Model is one model from GET /v1/models.
type Model struct {
	// ID is the model ID requests use, such as "kimi-k3".
	ID          string
	Name        string
	Description string
	Family      string
	// Type is the model kind; "chat" for text generation.
	Type        string
	Created     int64
	ReleaseDate string
	OpenWeights bool
	// Inputs are the accepted input kinds: text, image, video, audio, pdf.
	Inputs []string
	// Tools and Reasoning report tool calling and reasoning support;
	// ReasoningEfforts lists the named effort levels the model accepts.
	Tools            bool
	Reasoning        bool
	ReasoningEfforts []string
	ContextLength    int64
	MaxOutput        int64
	// NativeAPI is the API family the model is served by natively
	// (chat-completions, responses, messages or generate-content); Console
	// translates requests on the other APIs.
	NativeAPI string
	Price     Pricing
}

// Pricing is a model's text price in US dollars per million tokens.
type Pricing struct {
	Known      bool
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	// LongAbove is the context length above which LongInput and LongOutput
	// apply instead; 0 when the price does not depend on the context.
	LongAbove  int64
	LongInput  float64
	LongOutput float64
	// PeakFactor multiplies the price at peak hours; 0 when there is none.
	PeakFactor float64
}

// Free reports a model that costs nothing.
func (p Pricing) Free() bool {
	return p.Known && p.Input == 0 && p.Output == 0 && p.CacheRead == 0 && p.CacheWrite == 0
}

// Has reports whether the model accepts an input kind.
func (m Model) Has(input string) bool {
	return slices.Contains(m.Inputs, input)
}

// Chat reports whether the model generates text. Other kinds (none in the
// current lists) are left out when a key registers all models.
func (m Model) Chat() bool {
	return m.Type == "" || m.Type == "chat"
}

type modelsResponse struct {
	Data []struct {
		ID          string `json:"id"`
		Created     int64  `json:"created"`
		Name        string `json:"name"`
		Type        string `json:"type"`
		Description string `json:"description"`
		Family      string `json:"family"`
		OpenWeights bool   `json:"open_weights"`
		ReleaseDate string `json:"release_date"`
		Modalities  struct {
			Input []string `json:"input"`
		} `json:"modalities"`
		Capabilities struct {
			Tools struct {
				Supported bool `json:"supported"`
			} `json:"tools"`
			Reasoning struct {
				Supported bool     `json:"supported"`
				Effort    []string `json:"effort"`
			} `json:"reasoning"`
		} `json:"capabilities"`
		Limit struct {
			Context int64 `json:"context"`
			Output  int64 `json:"output"`
		} `json:"limit"`
		Cost map[string]any             `json:"cost"`
		API  map[string]json.RawMessage `json:"api"`
	} `json:"data"`
}

// Snapshot is an immutable view of the cached catalog.
type Snapshot struct {
	Models    []Model
	byID      map[string]Model
	Revision  string
	FetchedAt time.Time
	CheckedAt time.Time
	LastError string
}

// Empty reports whether no catalog has been loaded.
func (c Snapshot) Empty() bool { return len(c.Models) == 0 }

// Summary describes the snapshot on the management page.
func (c Snapshot) Summary() provider.CatalogSummary {
	return provider.CatalogSummary{Count: len(c.Models), Revision: c.Revision, FetchedAt: c.FetchedAt, CheckedAt: c.CheckedAt, LastError: c.LastError}
}

// Has reports whether a model ID is in the catalog.
func (c Snapshot) Has(id string) bool {
	_, ok := c.byID[id]
	return ok
}

// Lookup finds a model by its ID.
func (c Snapshot) Lookup(id string) (Model, bool) {
	model, ok := c.byID[id]
	return model, ok
}

// Catalog caches OpenCode's model list. The endpoint sends no ETag, so every
// refresh reloads it.
type Catalog struct {
	mu       sync.RWMutex
	snapshot Snapshot
	// fetchMu serializes upstream fetches so concurrent model.for_auth calls
	// during startup share one request.
	fetchMu sync.Mutex
}

// NewCatalog returns an empty catalog.
func NewCatalog() *Catalog {
	return &Catalog{snapshot: Snapshot{byID: map[string]Model{}}}
}

// Current returns the cached snapshot.
func (c *Catalog) Current() provider.Catalog {
	return c.current()
}

func (c *Catalog) current() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshot
}

// Fetch loads the catalog through client. force is accepted for symmetry with
// the refresh button; the list is always reloaded in full.
func (c *Catalog) Fetch(ctx context.Context, client *http.Client, baseURL, userAgent string, force bool, now time.Time) (provider.Catalog, error) {
	_ = force
	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()

	req, errReq := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+EndpointModels, nil)
	if errReq != nil {
		return c.current(), errReq
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, errDo := client.Do(req)
	if errDo != nil {
		return c.recordError(now, errDo)
	}
	body, errRead := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes))
	_ = resp.Body.Close()
	if errRead != nil {
		return c.recordError(now, errRead)
	}
	if resp.StatusCode != http.StatusOK {
		return c.recordError(now, fmt.Errorf("GET %s returned %d: %s", EndpointModels, resp.StatusCode, upstream.Truncate(string(body), 300)))
	}
	var list modelsResponse
	if errUnmarshal := json.Unmarshal(body, &list); errUnmarshal != nil {
		return c.recordError(now, fmt.Errorf("decode %s: %w", EndpointModels, errUnmarshal))
	}
	models := make([]Model, 0, len(list.Data))
	seen := make(map[string]struct{}, len(list.Data))
	for _, item := range list.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		model := Model{
			ID:            id,
			Name:          strings.TrimSpace(item.Name),
			Description:   strings.TrimSpace(item.Description),
			Family:        strings.TrimSpace(item.Family),
			Type:          strings.TrimSpace(item.Type),
			Created:       item.Created,
			ReleaseDate:   strings.TrimSpace(item.ReleaseDate),
			OpenWeights:   item.OpenWeights,
			Inputs:        inputKinds(item.Modalities.Input),
			Tools:         item.Capabilities.Tools.Supported,
			Reasoning:     item.Capabilities.Reasoning.Supported,
			ContextLength: item.Limit.Context,
			MaxOutput:     item.Limit.Output,
			Price:         pricingFrom(item.Cost),
		}
		for _, effort := range item.Capabilities.Reasoning.Effort {
			if effort = strings.TrimSpace(effort); effort != "" {
				model.ReasoningEfforts = append(model.ReasoningEfforts, effort)
			}
		}
		apis := make([]string, 0, len(item.API))
		for name := range item.API {
			apis = append(apis, name)
		}
		sort.Strings(apis)
		if len(apis) > 0 {
			model.NativeAPI = apis[0]
		}
		models = append(models, model)
	}
	if len(models) == 0 {
		return c.recordError(now, fmt.Errorf("%s returned no models", EndpointModels))
	}
	sort.Slice(models, func(i, j int) bool { return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID) })

	byID := make(map[string]Model, len(models))
	for _, model := range models {
		byID[model.ID] = model
	}
	next := Snapshot{
		Models:    models,
		byID:      byID,
		Revision:  catalogRevision(models),
		FetchedAt: now,
		CheckedAt: now,
	}
	c.mu.Lock()
	c.snapshot = next
	c.mu.Unlock()
	return next, nil
}

// inputKinds reduces MIME types ("image/png", "video/mp4", "application/pdf")
// to the kinds of input a model accepts, text first.
func inputKinds(mimes []string) []string {
	kinds := map[string]bool{}
	for _, mime := range mimes {
		mime = strings.ToLower(strings.TrimSpace(mime))
		switch {
		case mime == "text" || strings.HasPrefix(mime, "text/"):
			kinds["text"] = true
		case mime == "image" || strings.HasPrefix(mime, "image/"):
			kinds["image"] = true
		case mime == "video" || strings.HasPrefix(mime, "video/"):
			kinds["video"] = true
		case mime == "audio" || strings.HasPrefix(mime, "audio/"):
			kinds["audio"] = true
		case mime == "pdf" || mime == "application/pdf":
			kinds["pdf"] = true
		}
	}
	var out []string
	for _, kind := range []string{"text", "image", "video", "audio", "pdf"} {
		if kinds[kind] {
			out = append(out, kind)
		}
	}
	if len(out) == 0 {
		out = []string{"text"}
	}
	return out
}

// pricingFrom reads the text price of a model:
// {"text":{"input":{"price","cache":{"read","write"},"overrides":[{"when":{"context":{"gt"}},"price"}]},
// "output":{"price","overrides":[...]}},"multipliers":[{"when":...,"factor"}]}.
// A cache write priced per lifetime ({"30m","12h"}) reads the shortest one.
func pricingFrom(cost map[string]any) Pricing {
	text, _ := cost["text"].(map[string]any)
	input, _ := text["input"].(map[string]any)
	output, _ := text["output"].(map[string]any)
	inPrice, okIn := number(input["price"])
	outPrice, okOut := number(output["price"])
	if !okIn && !okOut {
		return Pricing{}
	}
	price := Pricing{Known: true, Input: inPrice, Output: outPrice}
	if cache, ok := input["cache"].(map[string]any); ok {
		price.CacheRead, _ = number(cache["read"])
		price.CacheWrite = cacheWrite(cache["write"])
	}
	if above, longIn, ok := firstOverride(input); ok {
		price.LongAbove, price.LongInput = above, longIn
		price.LongOutput = outPrice
		if outAbove, longOut, okOut := firstOverride(output); okOut && outAbove == above {
			price.LongOutput = longOut
		}
	}
	if multipliers, ok := cost["multipliers"].([]any); ok {
		for _, raw := range multipliers {
			entry, _ := raw.(map[string]any)
			if factor, okFactor := number(entry["factor"]); okFactor && factor > price.PeakFactor {
				price.PeakFactor = factor
			}
		}
	}
	return price
}

func cacheWrite(raw any) float64 {
	if value, ok := number(raw); ok {
		return value
	}
	byLifetime, _ := raw.(map[string]any)
	for _, key := range []string{"5m", "30m", "1h", "12h"} {
		if value, ok := number(byLifetime[key]); ok {
			return value
		}
	}
	return 0
}

// firstOverride reads the first context-length tier of a price.
func firstOverride(side map[string]any) (int64, float64, bool) {
	overrides, _ := side["overrides"].([]any)
	for _, raw := range overrides {
		entry, _ := raw.(map[string]any)
		when, _ := entry["when"].(map[string]any)
		tier, _ := when["context"].(map[string]any)
		above, okAbove := number(tier["gt"])
		value, okValue := number(entry["price"])
		if okAbove && okValue {
			return int64(above), value, true
		}
	}
	return 0, 0, false
}

func number(raw any) (float64, bool) {
	value, ok := raw.(float64)
	return value, ok
}

func (c *Catalog) recordError(now time.Time, err error) (provider.Catalog, error) {
	c.mu.Lock()
	c.snapshot.CheckedAt = now
	c.snapshot.LastError = err.Error()
	snapshot := c.snapshot
	c.mu.Unlock()
	return snapshot, err
}

// catalogRevision hashes the parts of the catalog that end up in CPA's model
// registry, so only changes there (not price changes) trigger
// re-registration.
func catalogRevision(models []Model) string {
	hash := sha256.New()
	for _, model := range models {
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%d\x00%d\x00%d\x00%s\x00%t\x00%t\x00%s\n",
			model.ID, model.Name, model.ContextLength, model.MaxOutput, model.Created,
			strings.Join(model.Inputs, ","), model.Tools, model.Reasoning, strings.Join(model.ReasoningEfforts, ","))
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}

// ModelInfos builds the CPA model list for one credential: every chat model
// of the catalog when all is set, otherwise the selected IDs. Selected IDs
// missing from the catalog are still registered (with minimal metadata) so a
// catalog outage or a retired model does not silently drop routes the user
// picked. IDs are the bare upstream IDs; CPA adds the auth's prefix itself.
func (c Snapshot) ModelInfos(all bool, ids []string) []pluginapi.ModelInfo {
	var selected []Model
	if all {
		selected = make([]Model, 0, len(c.Models))
		for _, model := range c.Models {
			if model.Chat() {
				selected = append(selected, model)
			}
		}
	} else {
		selected = make([]Model, 0, len(ids))
		for _, id := range ids {
			if model, ok := c.Lookup(id); ok {
				selected = append(selected, model)
				continue
			}
			selected = append(selected, Model{ID: id})
		}
	}
	out := make([]pluginapi.ModelInfo, 0, len(selected))
	for _, model := range selected {
		out = append(out, toModelInfo(model))
	}
	return out
}

func toModelInfo(model Model) pluginapi.ModelInfo {
	inputs := model.Inputs
	if len(inputs) == 0 {
		inputs = []string{"text"}
	}
	var params []string
	if model.Tools {
		params = append(params, "tools")
	}
	if model.Reasoning {
		params = append(params, "reasoning")
	}
	name := model.Name
	if name == "" {
		name = model.ID
	}
	description := model.Description
	if description == "" {
		description = "OpenCode · " + model.ID
	}
	info := pluginapi.ModelInfo{
		ID:                        model.ID,
		Object:                    "model",
		Created:                   model.Created,
		OwnedBy:                   ProviderID,
		Type:                      ProviderID,
		DisplayName:               name,
		Name:                      model.ID,
		Description:               description,
		InputTokenLimit:           model.ContextLength,
		OutputTokenLimit:          model.MaxOutput,
		ContextLength:             model.ContextLength,
		MaxCompletionTokens:       model.MaxOutput,
		SupportedParameters:       params,
		SupportedInputModalities:  append([]string(nil), inputs...),
		SupportedOutputModalities: []string{"text"},
		// UserDefined keeps CPA from validating or stripping reasoning
		// settings; OpenCode maps them per model.
		UserDefined: true,
	}
	if len(model.ReasoningEfforts) > 0 {
		info.Thinking = &pluginapi.ThinkingSupport{
			Levels:      append([]string(nil), model.ReasoningEfforts...),
			ZeroAllowed: slices.Contains(model.ReasoningEfforts, "none"),
		}
	}
	return info
}

// UpstreamID maps the model CPA hands the executor back to OpenCode's ID.
// CPA strips the auth prefix before calling the executor; the prefix is
// still removed here in case a model arrives with it.
func (c Snapshot) UpstreamID(model, prefix string) string {
	model = stripThinkingSuffix(strings.TrimSpace(model))
	if prefix != "" && strings.HasPrefix(model, prefix+"/") {
		return strings.TrimPrefix(model, prefix+"/")
	}
	if _, ok := c.Lookup(model); ok {
		return model
	}
	// A prefix we do not know (for example from a changed config) is dropped
	// when what follows it is a catalog model.
	if idx := strings.LastIndex(model, "/"); idx >= 0 && idx < len(model)-1 {
		if _, ok := c.Lookup(model[idx+1:]); ok {
			return model[idx+1:]
		}
	}
	return model
}

// stripThinkingSuffix removes a trailing CPA reasoning suffix such as
// "(high)" or "(8192)". CPA normally strips it before calling the executor.
func stripThinkingSuffix(model string) string {
	if !strings.HasSuffix(model, ")") {
		return model
	}
	open := strings.LastIndex(model, "(")
	if open <= 0 {
		return model
	}
	return strings.TrimSpace(model[:open])
}

// modelView is one model of the management page's models view.
type modelView struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Family      string `json:"family,omitempty"`
	// Chat is false for models that do not generate text; keys that register
	// all models leave them out.
	Chat             bool         `json:"chat"`
	Inputs           []string     `json:"inputs,omitempty"`
	Tools            bool         `json:"tools,omitempty"`
	Reasoning        bool         `json:"reasoning,omitempty"`
	ReasoningEfforts []string     `json:"reasoning_efforts,omitempty"`
	ContextLength    int64        `json:"context_length,omitempty"`
	MaxOutput        int64        `json:"max_output,omitempty"`
	NativeAPI        string       `json:"native_api,omitempty"`
	OpenWeights      bool         `json:"open_weights,omitempty"`
	ReleaseDate      string       `json:"release_date,omitempty"`
	Pricing          *pricingView `json:"pricing,omitempty"`
}

// pricingView is a model's text price in US dollars per million tokens.
type pricingView struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
	LongAbove  int64   `json:"long_above,omitempty"`
	LongInput  float64 `json:"long_input,omitempty"`
	LongOutput float64 `json:"long_output,omitempty"`
	PeakFactor float64 `json:"peak_factor,omitempty"`
	Free       bool    `json:"free,omitempty"`
}

// ModelsView lists the models for the management page.
func (c Snapshot) ModelsView() any {
	out := make([]modelView, 0, len(c.Models))
	for _, model := range c.Models {
		item := modelView{
			ID:               model.ID,
			Name:             model.Name,
			Description:      model.Description,
			Family:           model.Family,
			Chat:             model.Chat(),
			Inputs:           model.Inputs,
			Tools:            model.Tools,
			Reasoning:        model.Reasoning,
			ReasoningEfforts: model.ReasoningEfforts,
			ContextLength:    model.ContextLength,
			MaxOutput:        model.MaxOutput,
			NativeAPI:        model.NativeAPI,
			OpenWeights:      model.OpenWeights,
			ReleaseDate:      model.ReleaseDate,
		}
		if price := model.Price; price.Known {
			item.Pricing = &pricingView{
				Input:      price.Input,
				Output:     price.Output,
				CacheRead:  price.CacheRead,
				CacheWrite: price.CacheWrite,
				LongAbove:  price.LongAbove,
				LongInput:  price.LongInput,
				LongOutput: price.LongOutput,
				PeakFactor: price.PeakFactor,
				Free:       price.Free(),
			}
		}
		out = append(out, item)
	}
	return out
}
