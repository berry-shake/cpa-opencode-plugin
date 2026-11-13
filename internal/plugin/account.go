package plugin

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/berry-shake/cpa-opencode-plugin/internal/credential"
	"github.com/berry-shake/cpa-opencode-plugin/internal/opencode"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const (
	accountCallTimeout = 20 * time.Second
	// accountCacheTTL is how long a Go usage answer is reused. The endpoint
	// documents no rate limit; OpenCode's own clients poll it about once a
	// minute.
	accountCacheTTL = time.Minute
)

type rpcQuotaFetchRequest struct {
	pluginapi.QuotaFetchRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// OpenCode account queries (Go usage and the check made when a key is saved)
// go out one at a time per egress: a query waits for the one ahead of
// it on the same proxy, or the same direct line, to finish. Keys on different
// egresses are queried in parallel.

// accountTurn resolves the egress for a credential's proxy and waits for
// its turn, giving up when ctx ends. release must be called when done.
func (s *Service) accountTurn(ctx context.Context, proxyURL string) (client *http.Client, release func(), perr *pluginabi.Error) {
	setting, errProxy := opencode.ResolveEgress(proxyURL, s.globalProxy())
	if errProxy != nil {
		return nil, nil, opencode.RequestError("invalid_proxy", errProxy.Error())
	}
	release, errWait := s.accountQueue.Acquire(ctx, setting)
	if errWait != nil {
		return nil, nil, opencode.TransportError(fmt.Errorf("waiting for the egress: %w", errWait))
	}
	return s.clients.Client(setting), release, nil
}

// goUsageURL is where a credential's Go usage is read.
func (s *Service) goUsageURL(cred credential.Credential) string {
	return opencode.GoUsageURL(cred.EffectiveBaseURL(s.config()))
}

// checkKey verifies a key with the Go usage endpoint, in its egress's turn.
// A 403 means the key works but its workspace has no Go subscription, which
// still leaves pay-as-you-go models, so only other errors fail the check.
func (s *Service) checkKey(ctx context.Context, cred credential.Credential) *pluginabi.Error {
	client, release, perr := s.accountTurn(ctx, cred.ProxyURL)
	if perr != nil {
		return perr
	}
	defer release()
	_, perr = opencode.GetJSON(ctx, client, s.goUsageURL(cred), cred.APIKey, s.userAgent(), opencode.EndpointGoUsage)
	if perr != nil && perr.HTTPStatus == http.StatusForbidden {
		return nil
	}
	return perr
}

// fetchAccount loads the Go usage of the workspace behind a key, in a turn of
// the key's egress; time spent waiting for the turn does not count toward the
// call's timeout. A complete answer (usage, or "no Go subscription") is
// reused for accountCacheTTL.
func (s *Service) fetchAccount(cred credential.Credential) (opencode.AccountInfo, *pluginabi.Error) {
	target := s.goUsageURL(cred)
	cacheKey := credential.Fingerprint(cred.APIKey) + " " + target
	if info, ok := s.accounts.get(cacheKey, s.now()); ok {
		return info, nil
	}
	client, release, perr := s.accountTurn(s.ctx, cred.ProxyURL)
	if perr != nil {
		return opencode.AccountInfo{FetchedAt: s.now().UTC().Format(time.RFC3339)}, perr
	}
	defer release()
	// The query ahead in the queue may have been for the same key.
	if info, ok := s.accounts.get(cacheKey, s.now()); ok {
		return info, nil
	}
	ctx, cancel := context.WithTimeout(s.ctx, accountCallTimeout)
	raw, perr := opencode.GetJSON(ctx, client, target, cred.APIKey, s.userAgent(), opencode.EndpointGoUsage)
	cancel()

	info := opencode.AccountInfo{FetchedAt: s.now().UTC().Format(time.RFC3339)}
	switch {
	case perr != nil && perr.HTTPStatus == http.StatusForbidden:
		info.NoGo = true
	case perr != nil:
		info.Error = perr.Message
		return info, perr
	default:
		info.Go = opencode.GoUsageFrom(raw)
		info.Account = opencode.AccountFingerprint(info.Go)
	}
	s.accounts.put(cacheKey, info, s.now())
	return info, nil
}

// quotaFetch does not call rememberHost: CPA sends quota requests without the
// host summary, and an empty one would wipe the cached global proxy.
func (s *Service) quotaFetch(req pluginapi.QuotaFetchRequest) []byte {
	cred, _, errCred := credential.FromMaps(req.StorageJSON, req.Metadata)
	if errCred != nil || cred.APIKey == "" {
		// Fall back to the auth file on disk when CPA sends no storage.
		if fileCred, ok := s.credentialByAuthIndex(req.AuthIndex); ok {
			cred = fileCred
		}
	}
	if cred.APIKey == "" {
		return errorEnvelopeFrom(opencode.AuthError("opencode: credential has no api_key"))
	}
	info, perr := s.fetchAccount(cred)
	if perr != nil {
		return errorEnvelopeFrom(perr)
	}
	return okEnvelope(opencode.QuotaFromAccount(info))
}

// accountCache keeps complete account answers per key and origin.
type accountCache struct {
	mu      sync.Mutex
	entries map[string]accountCacheEntry
}

type accountCacheEntry struct {
	info opencode.AccountInfo
	at   time.Time
}

func (c *accountCache) get(key string, now time.Time) (opencode.AccountInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || now.Before(entry.at) || now.Sub(entry.at) >= accountCacheTTL {
		return opencode.AccountInfo{}, false
	}
	return entry.info, true
}

func (c *accountCache) put(key string, info opencode.AccountInfo, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]accountCacheEntry)
	}
	for other, entry := range c.entries {
		if now.Sub(entry.at) >= accountCacheTTL {
			delete(c.entries, other)
		}
	}
	c.entries[key] = accountCacheEntry{info: info, at: now}
}
