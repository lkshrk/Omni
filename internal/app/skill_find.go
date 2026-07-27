package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/agent"
)

const (
	defaultSkillsCatalogURL = "https://skills.sh/api/search"
	catalogCacheTTL         = time.Hour
	catalogSearchLimit      = 10
)

// Read per lookup so a test process can point searches at a stub server after the App was built.
func skillsCatalogEndpoint() string {
	if base := strings.TrimSpace(os.Getenv("OMNI_SKILLS_CATALOG_URL")); base != "" {
		return base
	}
	return defaultSkillsCatalogURL
}

// The override reaches production, and the query terms are the user's words, so plaintext is allowed only against a loopback stub.
func validateCatalogEndpoint(u *url.URL) error {
	if u.Host == "" {
		return fmt.Errorf("skills catalog url %q has no host", u.Redacted())
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
	}
	return fmt.Errorf("skills catalog url %q must use https", u.Redacted())
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// A catalog reached over a black-holed connection would otherwise hang the search forever, since both callers pass a deadline-less context.
func catalogHTTPClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return validateCatalogEndpoint(req.URL)
		},
	}
}

type FindResult struct {
	Source   string
	Skill    string
	Installs string
}

type catalogState interface {
	GetState(context.Context, string) (string, error)
	SetState(context.Context, string, string) error
}

type catalogCache struct {
	FetchedAt time.Time    `json:"fetched_at"`
	Results   []FindResult `json:"results"`
}

type CatalogWarning struct {
	Cause error
}

func (w *CatalogWarning) Error() string { return "using stale skills catalog: " + w.Cause.Error() }
func (w *CatalogWarning) Unwrap() error { return w.Cause }

func IsCatalogWarning(err error) bool {
	var warning *CatalogWarning
	return errors.As(err, &warning)
}

func findSkillPackages(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	state catalogState,
	query string,
	owner string,
) ([]FindResult, error) {
	normalized := strings.ToLower(strings.Join(strings.Fields(query), " "))
	if normalized == "" {
		return nil, fmt.Errorf("skill search query is required")
	}
	owner = strings.TrimSpace(owner)
	cacheKey := catalogCacheKey(endpoint, normalized, owner)
	cached, hasCache := loadCatalogCache(ctx, state, cacheKey)
	// A clock that jumped backwards would otherwise make an entry stamped in the future immortal.
	if age := time.Since(cached.FetchedAt); hasCache && age >= 0 && age < catalogCacheTTL {
		return cached.Results, nil
	}
	results, err := fetchCatalog(ctx, client, endpoint, normalized, owner)
	if err != nil {
		if hasCache {
			return cached.Results, &CatalogWarning{Cause: err}
		}
		return nil, err
	}
	if state != nil {
		// Best-effort cache write; a failure only costs a re-fetch next search.
		if data, err := json.Marshal(catalogCache{FetchedAt: time.Now().UTC(), Results: results}); err == nil {
			_ = state.SetState(ctx, cacheKey, string(data))
		}
	}
	return results, nil
}

// FindSkillPackages — A non-empty owner narrows results to that GitHub owner.
func (a *App) FindSkillPackages(ctx context.Context, query, owner string) ([]FindResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	if err := a.requireSkillsEnabled(cfg); err != nil {
		return nil, err
	}
	var state catalogState
	if db := a.readDB(); db != nil {
		state = db
	}
	return findSkillPackages(ctx, a.httpClient, skillsCatalogEndpoint(), state, query, owner)
}

// Rows are skipped individually so one malformed entry does not cost the whole result set.
func fetchCatalog(ctx context.Context, client *http.Client, endpoint, query, owner string) ([]FindResult, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if err := validateCatalogEndpoint(u); err != nil {
		return nil, err
	}
	client = catalogHTTPClient(client)
	values := u.Query()
	values.Set("q", query)
	values.Set("limit", strconv.Itoa(catalogSearchLimit))
	if owner != "" {
		values.Set("owner", owner)
	}
	u.RawQuery = values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searching skills catalog: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("searching skills catalog: HTTP %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("skills catalog response exceeds 1 MiB")
	}
	var payload struct {
		Skills []struct {
			Source   string `json:"source"`
			SkillID  string `json:"skillId"`
			Name     string `json:"name"`
			Installs int64  `json:"installs"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("invalid skills catalog response: %w", err)
	}
	if payload.Skills == nil {
		return nil, fmt.Errorf("invalid skills catalog response")
	}
	if len(payload.Skills) > catalogSearchLimit {
		payload.Skills = payload.Skills[:catalogSearchLimit]
	}
	results := make([]FindResult, 0, len(payload.Skills))
	for _, item := range payload.Skills {
		skill := item.SkillID
		if skill == "" {
			skill = item.Name
		}
		parsed, err := agent.ParseSkillSource(item.Source + "@" + skill)
		if err != nil || parsed.Source != item.Source || len(parsed.Skills) != 1 || item.Installs < 0 {
			continue
		}
		results = append(results, FindResult{
			Source:   item.Source,
			Skill:    skill,
			Installs: strconv.FormatInt(item.Installs, 10) + " installs",
		})
	}
	if len(results) == 0 && len(payload.Skills) > 0 {
		return nil, fmt.Errorf("invalid skills catalog result")
	}
	return results, nil
}

// Keyed by endpoint too: pointing OMNI_SKILLS_CATALOG_URL at another catalog must not keep serving the previous one's rows for the rest of the TTL.
func catalogCacheKey(endpoint, query, owner string) string {
	sum := sha256.Sum256([]byte(endpoint + "\x00" + owner + "\x00" + query))
	return "skills_catalog.search." + hex.EncodeToString(sum[:])
}

func loadCatalogCache(ctx context.Context, state catalogState, key string) (catalogCache, bool) {
	if state == nil {
		return catalogCache{}, false
	}
	raw, err := state.GetState(ctx, key)
	if err != nil {
		return catalogCache{}, false
	}
	var cached catalogCache
	if json.Unmarshal([]byte(raw), &cached) != nil || cached.FetchedAt.IsZero() || cached.Results == nil {
		return catalogCache{}, false
	}
	return cached, true
}
