package clashplugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	ruleCacheTTL               = 24 * time.Hour
	ruleDownloadMaxConcurrency = 4
)

type ruleHTTPResponse struct {
	body         string
	etag         string
	lastModified string
	notModified  bool
}

type ruleDiskCacheEntry struct {
	URL          string    `json:"url"`
	Rules        []string  `json:"rules"`
	FetchedAt    time.Time `json:"fetched_at"`
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
}

type ruleFetchResult struct {
	rules []string
	stale bool
	err   error
}

func (c *subscriptionConverter) prefetchINIRuleHosts(ctx context.Context) map[string]ruleFetchResult {
	results := make(map[string]ruleFetchResult)
	if c.ini == nil {
		return results
	}
	var urls []string
	seen := make(map[string]bool)
	for _, source := range c.ini.Rules {
		rawURL := source.Source
		if strings.HasPrefix(rawURL, "[]") || seen[rawURL] {
			continue
		}
		seen[rawURL] = true
		urls = append(urls, rawURL)
	}
	if len(urls) == 0 {
		return results
	}

	jobs := make(chan string)
	var mu sync.Mutex
	var workers sync.WaitGroup
	workerCount := min(ruleDownloadMaxConcurrency, len(urls))
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for rawURL := range jobs {
				rules, err := c.fetchRuleHost(ctx, rawURL)
				mu.Lock()
				results[rawURL] = ruleFetchResult{rules: rules, err: err}
				mu.Unlock()
			}
		}()
	}
	for _, rawURL := range urls {
		jobs <- rawURL
	}
	close(jobs)
	workers.Wait()
	return results
}

func (c *subscriptionConverter) ruleMemoryCacheKey(rawURL string) string {
	return filepath.Clean(c.ruleCacheDir) + "\x00" + strings.TrimSpace(rawURL)
}

func (c *subscriptionConverter) fetchAndCacheRuleHost(ctx context.Context, rawURL, memoryKey string) (ruleFetchResult, error) {
	diskEntry, diskErr := c.loadRuleDiskCache(rawURL)
	if diskErr != nil {
		log.Printf("[clash] ignore invalid rule cache for %s: %v", rawURL, diskErr)
	}
	if diskEntry != nil && time.Since(diskEntry.FetchedAt) < ruleCacheTTL {
		expiresAt := diskEntry.FetchedAt.Add(ruleCacheTTL)
		setCachedRuleHostUntil(memoryKey, diskEntry.Rules, expiresAt)
		return ruleFetchResult{rules: diskEntry.Rules}, nil
	}

	etag, lastModified := "", ""
	if diskEntry != nil {
		etag, lastModified = diskEntry.ETag, diskEntry.LastModified
	}
	response, err := c.fetchRuleTextConditional(ctx, rawURL, etag, lastModified)
	if err != nil && c.ruleProxyURL != nil && c.isConfiguredRuleURL(rawURL) && ctx.Err() == nil {
		response, err = c.retryRuleThroughProxyConditional(ctx, rawURL, etag, lastModified, err)
	}
	if err != nil {
		if diskEntry != nil {
			log.Printf("[clash] rule update failed for %s; using stale cache: %v", rawURL, err)
			return ruleFetchResult{rules: diskEntry.Rules, stale: true}, nil
		}
		return ruleFetchResult{}, err
	}

	now := time.Now()
	if response.notModified {
		if diskEntry == nil {
			return ruleFetchResult{}, fmt.Errorf("rule server returned 304 without a cached copy")
		}
		diskEntry.FetchedAt = now
		if response.etag != "" {
			diskEntry.ETag = response.etag
		}
		if response.lastModified != "" {
			diskEntry.LastModified = response.lastModified
		}
		if err := c.saveRuleDiskCache(*diskEntry); err != nil {
			log.Printf("[clash] save refreshed rule cache %s: %v", rawURL, err)
		}
		setCachedRuleHost(memoryKey, diskEntry.Rules)
		return ruleFetchResult{rules: diskEntry.Rules}, nil
	}

	rules := parseRuleHostContent(path.Ext(mustURLPath(rawURL)), []byte(response.body))
	entry := ruleDiskCacheEntry{
		URL:          rawURL,
		Rules:        rules,
		FetchedAt:    now,
		ETag:         response.etag,
		LastModified: response.lastModified,
	}
	if err := c.saveRuleDiskCache(entry); err != nil {
		log.Printf("[clash] save rule cache %s: %v", rawURL, err)
	}
	setCachedRuleHost(memoryKey, rules)
	return ruleFetchResult{rules: rules}, nil
}

func (c *subscriptionConverter) fetchRuleTextConditional(ctx context.Context, rawURL, etag, lastModified string) (ruleHTTPResponse, error) {
	if !c.allowPrivateFetch {
		if err := validatePublicFetchURL(ctx, rawURL); err != nil {
			return ruleHTTPResponse{}, err
		}
	}
	return fetchRuleTextConditional(ctx, c.client, rawURL, etag, lastModified)
}

func (c *subscriptionConverter) ruleCachePath(rawURL string) string {
	if strings.TrimSpace(c.ruleCacheDir) == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(strings.TrimSpace(rawURL)))
	return filepath.Join(c.ruleCacheDir, hex.EncodeToString(hash[:])+".json")
}

func (c *subscriptionConverter) loadRuleDiskCache(rawURL string) (*ruleDiskCacheEntry, error) {
	cachePath := c.ruleCachePath(rawURL)
	if cachePath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(cachePath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entry ruleDiskCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, err
	}
	if entry.URL != strings.TrimSpace(rawURL) || entry.FetchedAt.IsZero() || entry.Rules == nil {
		return nil, fmt.Errorf("cache metadata does not match URL")
	}
	return &entry, nil
}

func (c *subscriptionConverter) saveRuleDiskCache(entry ruleDiskCacheEntry) error {
	cachePath := c.ruleCachePath(entry.URL)
	if cachePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(cachePath), ".rule-cache-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, cachePath)
}

func (c *subscriptionConverter) addSubscriptionWarning(warning string) {
	c.ruleWarningMu.Lock()
	defer c.ruleWarningMu.Unlock()
	c.ruleWarnings = dedupeStringList(append(c.ruleWarnings, warning))
}

func (c *subscriptionConverter) subscriptionWarnings() []string {
	c.ruleWarningMu.Lock()
	defer c.ruleWarningMu.Unlock()
	return append([]string(nil), c.ruleWarnings...)
}
