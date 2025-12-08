package payloadstorage

import (
	"context"
	"os"

	"decentralized-api/logging"

	"github.com/productscience/inference/x/inference/types"
)

// NewPayloadStorage creates a PayloadStorage based on environment configuration.
// If PGHOST is set and PostgreSQL is accessible, uses HybridStorage (PG primary + file fallback).
// Otherwise, uses FileStorage only.
func NewPayloadStorage(ctx context.Context, fileBasePath string) PayloadStorage {
	fileStorage := NewFileStorage(fileBasePath)

	// Check if PostgreSQL is configured
	pgHost := os.Getenv("PGHOST")
	if pgHost == "" {
		logging.Info("PGHOST not set, using file storage only", types.PayloadStorage)
		return fileStorage
	}

	// Try to connect to PostgreSQL
	pgStorage, err := NewPostgresStorage(ctx)
	if err != nil {
		logging.Warn("PostgreSQL unavailable, using file storage only", types.PayloadStorage, "error", err)
		return fileStorage
	}

	logging.Info("Using PostgreSQL with file fallback", types.PayloadStorage, "host", pgHost)
	return NewHybridStorage(pgStorage, fileStorage)
}
