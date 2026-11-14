package opencode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/berry-shake/cpa-opencode-plugin/internal/jsonfield"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// AccountInfo is the Go allowance of the workspace behind one key. Every key
// of a workspace reports the same figures.
type AccountInfo struct {
	// Account identifies the figures, so the management page can count
	// workspaces without counting one once per key. The usage endpoint
	// reports no workspace ID; keys whose monthly reset and percentages read
	// the same are taken as one workspace. Empty without Go usage.
	Account string   `json:"account,omitempty"`
	Go      *GoUsage `json:"go,omitempty"`
	// NoGo is set when the key's workspace does not subscribe to Go (the
	// usage endpoint answers 403); the key may still use pay-as-you-go models.
	NoGo      bool   `json:"no_go,omitempty"`
	Error     string `json:"error,omitempty"`
	FetchedAt string `json:"fetched_at"`
}

// GoUsage is the Go allowance: how much of each limit is used. The limits
// are dollar amounts per model; the rolling window is 5 hours (20% of the
// monthly limit), the weekly one 50% and the monthly one 100%.
type GoUsage struct {
	Rolling *GoWindow `json:"rolling,omitempty"`
	Weekly  *GoWindow `json:"weekly,omitempty"`
	Monthly *GoWindow `json:"monthly,omitempty"`
}

// GoWindow is one limit window.
type GoWindow struct {
	// Percent is how much of the window's allowance is used, 0 to 100.
	Percent float64 `json:"percent"`
	// Limited is set when Go refuses requests until the window resets.
	Limited  bool   `json:"limited"`
	ResetsAt string `json:"resets_at,omitempty"`
}

// Left is the share of the window still available, 0 to 1.
func (w GoWindow) Left() float64 {
	if w.Limited {
		return 0
	}
	return math.Max(0, math.Min(1, 1-w.Percent/100))
}

// GetJSON GETs a JSON object from OpenCode with a bearer token. name labels
// the endpoint in errors.
func GetJSON(ctx context.Context, client *http.Client, target, token, userAgent, name string) (map[string]any, *pluginabi.Error) {
	req, errReq := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if errReq != nil {
		return nil, TransportError(errReq)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, errDo := client.Do(req)
	if errDo != nil {
		return nil, TransportError(errDo)
	}
	defer func() { _ = resp.Body.Close() }()
	body, errRead := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if errRead != nil {
		return nil, TransportError(errRead)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, UpstreamError(resp.StatusCode, resp.Header, body, name)
	}
	var out map[string]any
	if errUnmarshal := json.Unmarshal(body, &out); errUnmarshal != nil {
		return nil, &pluginabi.Error{Code: "invalid_upstream_response", Message: "opencode: decode " + name + ": " + errUnmarshal.Error(), HTTPStatus: http.StatusBadGateway}
	}
	return out, nil
}

// GoUsageFrom reads a GET /zen/go/v1/usage response:
// {"usage":{"rolling":{"status":"ok"|"rate-limited","percent","resetsAt"},"weekly":{...},"monthly":{...}}}.
// It returns nil when the response has no usage.
func GoUsageFrom(raw map[string]any) *GoUsage {
	usage, ok := raw["usage"].(map[string]any)
	if !ok {
		return nil
	}
	out := &GoUsage{
		Rolling: goWindowFrom(usage["rolling"]),
		Weekly:  goWindowFrom(usage["weekly"]),
		Monthly: goWindowFrom(usage["monthly"]),
	}
	if out.Rolling == nil && out.Weekly == nil && out.Monthly == nil {
		return nil
	}
	return out
}

func goWindowFrom(raw any) *GoWindow {
	fields, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	percent, _ := optionalNumber(fields["percent"])
	return &GoWindow{
		Percent:  math.Max(0, math.Min(100, percent)),
		Limited:  strings.EqualFold(jsonfield.String(fields, "status"), "rate-limited"),
		ResetsAt: jsonfield.String(fields, "resetsAt", "resets_at"),
	}
}

// AccountFingerprint names the workspace behind a Go usage reading. The
// monthly window resets on the subscription's own date and time, so it tells
// workspaces apart; the percentages separate the rare two that share it. The
// rolling window's reset is left out: without recent use it moves with the
// time of the request.
func AccountFingerprint(usage *GoUsage) string {
	if usage == nil || usage.Monthly == nil {
		return ""
	}
	parts := []string{usage.Monthly.ResetsAt}
	for _, window := range []*GoWindow{usage.Rolling, usage.Weekly, usage.Monthly} {
		if window == nil {
			parts = append(parts, "-")
			continue
		}
		parts = append(parts, strconv.FormatFloat(window.Percent, 'f', -1, 64))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:16]
}

// QuotaFromAccount renders account info for CPA's quota panel: one bar per
// Go window with the share left and its reset time.
func QuotaFromAccount(info AccountInfo) pluginapi.QuotaFetchResponse {
	var out pluginapi.QuotaFetchResponse
	if info.Go == nil {
		return out
	}
	var buckets []pluginapi.QuotaBucket
	for _, window := range []struct {
		name  string
		value *GoWindow
	}{{"5h", info.Go.Rolling}, {"7d", info.Go.Weekly}, {"monthly", info.Go.Monthly}} {
		if window.value == nil {
			continue
		}
		// No description: CPA's panel would show it in place of the window
		// name. A rate-limited window reads as 0% left.
		buckets = append(buckets, pluginapi.QuotaBucket{
			Window:            window.name,
			RemainingFraction: window.value.Left(),
			ResetTime:         window.value.ResetsAt,
		})
	}
	if len(buckets) > 0 {
		out.Groups = append(out.Groups, pluginapi.QuotaGroup{DisplayName: "OpenCode Go", Buckets: buckets})
	}
	return out
}

// optionalNumber accepts JSON numbers and decimal strings; null is absent.
func optionalNumber(raw any) (float64, bool) {
	switch value := raw.(type) {
	case float64:
		return value, true
	case string:
		parsed, errParse := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if errParse != nil {
			return 0, false
		}
		return parsed, true
	case json.Number:
		parsed, errParse := value.Float64()
		return parsed, errParse == nil
	default:
		return 0, false
	}
}
