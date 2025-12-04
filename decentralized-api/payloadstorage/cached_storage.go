package payloadstorage

import (
	"context"
	"sync"
	"time"
)

type cachedEntry struct {
	promptPayload   string
	responsePayload string
	expiresAt       time.Time
}

// CachedStorage wraps PayloadStorage with in-memory caching for Retrieve operations.
// Reduces disk I/O during validation bursts when multiple validators request same payload.
type CachedStorage struct {
	storage PayloadStorage
	mu      sync.RWMutex
	entries map[string]*cachedEntry
	ttl     time.Duration
}

func NewCachedStorage(storage PayloadStorage, ttl time.Duration) *CachedStorage {
	c := &CachedStorage{
		storage: storage,
		entries: make(map[string]*cachedEntry),
		ttl:     ttl,
	}
	go c.cleanupLoop()
	return c
}

func (c *CachedStorage) Store(ctx context.Context, inferenceId string, epochId uint64, promptPayload, responsePayload string) error {
	// Store to underlying storage
	if err := c.storage.Store(ctx, inferenceId, epochId, promptPayload, responsePayload); err != nil {
		return err
	}

	// Also cache it
	c.mu.Lock()
	c.entries[inferenceId] = &cachedEntry{
		promptPayload:   promptPayload,
		responsePayload: responsePayload,
		expiresAt:       time.Now().Add(c.ttl),
	}
	c.mu.Unlock()

	return nil
}

func (c *CachedStorage) Retrieve(ctx context.Context, inferenceId string, epochId uint64) (string, string, error) {
	// Check cache first
	c.mu.RLock()
	if cached, ok := c.entries[inferenceId]; ok && time.Now().Before(cached.expiresAt) {
		c.mu.RUnlock()
		return cached.promptPayload, cached.responsePayload, nil
	}
	c.mu.RUnlock()

	// Cache miss - retrieve from storage
	prompt, response, err := c.storage.Retrieve(ctx, inferenceId, epochId)
	if err != nil {
		return "", "", err
	}

	// Cache for next time
	c.mu.Lock()
	c.entries[inferenceId] = &cachedEntry{
		promptPayload:   prompt,
		responsePayload: response,
		expiresAt:       time.Now().Add(c.ttl),
	}
	c.mu.Unlock()

	return prompt, response, nil
}

func (c *CachedStorage) PruneEpoch(ctx context.Context, epochId uint64) error {
	return c.storage.PruneEpoch(ctx, epochId)
}

func (c *CachedStorage) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		c.cleanup()
	}
}

func (c *CachedStorage) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for id, cached := range c.entries {
		if now.After(cached.expiresAt) {
			delete(c.entries, id)
		}
	}
}

var _ PayloadStorage = (*CachedStorage)(nil)
