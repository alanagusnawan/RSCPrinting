package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/rscprinting/print-agent/pkg/apitypes"
	bolt "go.etcd.io/bbolt"
)

var bucketPendingResults = []byte("pending_results")

// ErrLocked menandakan DB sedang dipakai instance agent lain (file lock bbolt
// tidak bisa diambil dalam batas waktu).
var ErrLocked = errors.New("storage locked by another instance")

type Store struct {
	db *bolt.DB
	mu sync.Mutex
}

func Open(dataDir string) (*Store, error) {
	dbPath := filepath.Join(dataDir, "print-agent.db")
	db, err := bolt.Open(dbPath, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		if errors.Is(err, bolt.ErrTimeout) {
			return nil, fmt.Errorf("%w: %v", ErrLocked, err)
		}
		return nil, fmt.Errorf("open boltdb failed: %w", err)
	}

	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketPendingResults)
		return err
	})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create bucket failed: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Store) SavePendingResult(res apitypes.JobResultMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("marshal job result failed: %w", err)
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketPendingResults)
		return b.Put([]byte(res.JobID), data)
	})
}

func (s *Store) GetPendingResults() ([]apitypes.JobResultMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var results []apitypes.JobResultMessage
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketPendingResults)
		return b.ForEach(func(k, v []byte) error {
			var res apitypes.JobResultMessage
			if err := json.Unmarshal(v, &res); err == nil {
				results = append(results, res)
			}
			return nil
		})
	})
	return results, err
}

func (s *Store) DeletePendingResult(jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketPendingResults)
		return b.Delete([]byte(jobID))
	})
}
