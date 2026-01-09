package inventory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"multinic-operator/pkg/viola"
)

type Store struct {
	path string
	mu   sync.Mutex
	data map[string]Record
}

type Record struct {
	ProviderID     string           `json:"providerId"`
	NodeName       string           `json:"nodeName"`
	InstanceID     string           `json:"instanceId"`
	Config         viola.NodeConfig `json:"config"`
	LastConfigHash string           `json:"lastConfigHash"`
	UpdatedAt      time.Time        `json:"updatedAt"`
}

type fileData struct {
	Records []Record `json:"records"`
}

// NewStore는 파일 기반 인벤토리 저장소를 초기화한다.
func NewStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	store := &Store{
		path: path,
		data: make(map[string]Record),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

// Init은 파일 기반 저장소에서 별도 초기화가 필요 없음을 나타낸다.
func (s *Store) Init(_ context.Context) error {
	// No-op for file-based store; load is done in NewStore.
	return nil
}

// GetHash는 providerID+nodeName 기준으로 마지막 해시를 반환한다.
func (s *Store) GetHash(_ context.Context, providerID, nodeName string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.data[key(providerID, nodeName)]; ok {
		return rec.LastConfigHash, nil
	}
	return "", nil
}

// Upsert는 최신 NodeConfig를 저장하고 파일에 반영한다.
func (s *Store) Upsert(_ context.Context, providerID string, node viola.NodeConfig, hash string, updatedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key(providerID, node.NodeName)] = Record{
		ProviderID:     providerID,
		NodeName:       node.NodeName,
		InstanceID:     node.InstanceID,
		Config:         node,
		LastConfigHash: hash,
		UpdatedAt:      updatedAt.UTC(),
	}
	return s.persist()
}

// List는 조건(providerID/nodeName/instanceID)으로 레코드를 조회한다.
func (s *Store) List(_ context.Context, providerID, nodeName, instanceID string) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Record, 0)
	for _, rec := range s.data {
		if providerID != "" && rec.ProviderID != providerID {
			continue
		}
		if nodeName != "" && rec.NodeName != nodeName {
			continue
		}
		if instanceID != "" && rec.InstanceID != instanceID {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var payload fileData
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	for _, rec := range payload.Records {
		s.data[key(rec.ProviderID, rec.NodeName)] = rec
	}
	return nil
}

func (s *Store) persist() error {
	payload := fileData{Records: make([]Record, 0, len(s.data))}
	for _, rec := range s.data {
		payload.Records = append(payload.Records, rec)
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func key(providerID, nodeName string) string {
	return providerID + "|" + nodeName
}
