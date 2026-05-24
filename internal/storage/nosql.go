package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/oracle/nosql-go-sdk/nosqldb"
	"github.com/oracle/nosql-go-sdk/nosqldb/auth/iam"
	"github.com/oracle/nosql-go-sdk/nosqldb/types"
)

// NoSQLStorage talks to OCI NoSQL Database Cloud Service using Instance
// Principal authentication — no static credentials in the image or env.
//
// Required IAM setup:
//   - Dynamic Group matching the OKE worker nodes' compartment.
//   - Policy:
//       Allow dynamic-group <dg> to read   nosql-tables in compartment <c>
//       Allow dynamic-group <dg> to manage nosql-rows   in compartment <c>
type NoSQLStorage struct {
	client *nosqldb.Client
	table  string
}

// NewNoSQLStorage builds a client authed via Instance Principal.
// compartmentOCID scopes API calls; the OKE node identity must have
// permission to access that compartment.
func NewNoSQLStorage(endpoint, table, compartmentOCID string) (*NoSQLStorage, error) {
	provider, err := iam.NewSignatureProviderWithInstancePrincipal(compartmentOCID)
	if err != nil {
		return nil, fmt.Errorf("init OCI instance principal: %w", err)
	}

	cfg := nosqldb.Config{
		Endpoint:              endpoint,
		Mode:                  "cloud",
		AuthorizationProvider: provider,
	}

	client, err := nosqldb.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("create NoSQL client: %w", err)
	}

	return &NoSQLStorage{
		client: client,
		table:  table,
	}, nil
}

// SaveIfNotExists uses PutIfAbsent so collision detection is atomic on the
// database side — no read-then-write race.
func (s *NoSQLStorage) SaveIfNotExists(_ context.Context, u ShortURL) (bool, error) {
	val := types.NewMapValue(map[string]interface{}{
		"code":       u.Code,
		"long_url":   u.LongURL,
		"created_at": u.CreatedAt,
	})

	req := &nosqldb.PutRequest{
		TableName: s.table,
		Value:     val,
		PutOption: types.PutIfAbsent,
		Timeout:   3 * time.Second,
	}

	res, err := s.client.Put(req)
	if err != nil {
		return false, fmt.Errorf("NoSQL put-if-absent: %w", err)
	}
	// On collision the row is not written and Success() returns false.
	return res.Success(), nil
}

func (s *NoSQLStorage) Get(_ context.Context, code string) (*ShortURL, error) {
	key := types.NewMapValue(map[string]interface{}{
		"code": code,
	})

	req := &nosqldb.GetRequest{
		TableName: s.table,
		Key:       key,
		Timeout:   2 * time.Second,
	}

	res, err := s.client.Get(req)
	if err != nil {
		return nil, fmt.Errorf("NoSQL get: %w", err)
	}
	if res.Value == nil {
		return nil, ErrNotFound
	}

	longURL, _ := res.Value.GetString("long_url")
	createdAt, _ := res.Value.GetString("created_at")

	return &ShortURL{
		Code:      code,
		LongURL:   longURL,
		CreatedAt: createdAt,
	}, nil
}

// Ping issues a cheap metadata lookup — used by readiness probe.
func (s *NoSQLStorage) Ping(_ context.Context) error {
	req := &nosqldb.GetTableRequest{
		TableName: s.table,
		Timeout:   2 * time.Second,
	}
	_, err := s.client.GetTable(req)
	return err
}

func (s *NoSQLStorage) Close() error {
	if s.client == nil {
		return nil
	}
	return s.client.Close()
}