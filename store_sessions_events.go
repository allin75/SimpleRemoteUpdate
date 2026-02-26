package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type deploymentStore struct {
	mu   sync.Mutex
	file string
	list []Deployment
}

func newDeploymentStore(file string) (*deploymentStore, error) {
	s := &deploymentStore{
		file: file,
		list: make([]Deployment, 0),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *deploymentStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.list = []Deployment{}
			return nil
		}
		return err
	}
	if len(b) == 0 {
		s.list = []Deployment{}
		return nil
	}
	var out []Deployment
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	s.list = out
	return nil
}

func (s *deploymentStore) saveLocked() error {
	raw, err := json.MarshalIndent(s.list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.file + ".tmp"
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.file)
}

func (s *deploymentStore) Add(dep Deployment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = append(s.list, dep)
	return s.saveLocked()
}

func (s *deploymentStore) UpdateField(id string, fn func(dep *Deployment)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].ID == id {
			fn(&s.list[i])
			return s.saveLocked()
		}
	}
	return errors.New("deployment not found")
}

func (s *deploymentStore) Get(id string) (Deployment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].ID == id {
			return s.list[i], true
		}
	}
	return Deployment{}, false
}

func (s *deploymentStore) List() []Deployment {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Deployment, len(s.list))
	copy(out, s.list)
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

func (s *deploymentStore) SwitchFile(newFile string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if newFile == "" || newFile == s.file {
		return nil
	}

	oldFile := s.file
	oldList := make([]Deployment, len(s.list))
	copy(oldList, s.list)

	if err := os.MkdirAll(filepath.Dir(newFile), 0755); err != nil {
		return err
	}

	b, err := os.ReadFile(newFile)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		// 新文件不存在时，沿用当前内存记录并写入新文件
		s.file = newFile
		if err := s.saveLocked(); err != nil {
			s.file = oldFile
			s.list = oldList
			return err
		}
		return nil
	}

	var loaded []Deployment
	if len(b) > 0 {
		if err := json.Unmarshal(b, &loaded); err != nil {
			return err
		}
	}
	s.file = newFile
	s.list = loaded
	return nil
}

type sessionData struct {
	User      string
	ExpiresAt time.Time
}

type sessionManager struct {
	mu       sync.Mutex
	sessions map[string]sessionData
}

func newSessionManager() *sessionManager {
	return &sessionManager{
		sessions: make(map[string]sessionData),
	}
}

func (m *sessionManager) Create(user string, ttl time.Duration) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	token := randomHex(32)
	m.sessions[token] = sessionData{
		User:      user,
		ExpiresAt: time.Now().Add(ttl),
	}
	return token
}

func (m *sessionManager) Get(token string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.sessions[token]
	if !ok {
		return "", false
	}
	if time.Now().After(data.ExpiresAt) {
		delete(m.sessions, token)
		return "", false
	}
	return data.User, true
}

func (m *sessionManager) Delete(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, token)
}

type eventHub struct {
	mu     sync.Mutex
	nextID int
	subs   map[string]map[int]chan Event
}

func newEventHub() *eventHub {
	return &eventHub{
		subs: make(map[string]map[int]chan Event),
	}
}

func (h *eventHub) Subscribe(depID string) (int, <-chan Event, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	id := h.nextID
	if h.subs[depID] == nil {
		h.subs[depID] = make(map[int]chan Event)
	}
	ch := make(chan Event, 128)
	h.subs[depID][id] = ch
	unsubscribe := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if chMap, ok := h.subs[depID]; ok {
			if _, exists := chMap[id]; exists {
				delete(chMap, id)
			}
			if len(chMap) == 0 {
				delete(h.subs, depID)
			}
		}
	}
	return id, ch, unsubscribe
}

func (h *eventHub) Publish(depID string, evt Event) {
	h.mu.Lock()
	chMap := h.subs[depID]
	targets := make([]chan Event, 0, len(chMap))
	for _, ch := range chMap {
		targets = append(targets, ch)
	}
	h.mu.Unlock()
	for _, ch := range targets {
		select {
		case ch <- evt:
		default:
		}
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func newID(prefix string) string {
	return fmt.Sprintf("%s-%s-%s", prefix, time.Now().Format("20060102-150405"), randomHex(4))
}
