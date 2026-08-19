package main

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

var errObjectNotFound = errors.New("object not found")

type objectInfo struct {
	Key         string
	Size        int64
	ContentType string
	UpdatedAt   time.Time
}

type objectStore interface {
	put(context.Context, string, []byte, string) error
	get(context.Context, string) ([]byte, objectInfo, error)
	list(context.Context, string) ([]objectInfo, error)
	delete(context.Context, string) error
}

type memoryObjectStore struct {
	mu      sync.Mutex
	objects map[string]memoryObject
}

type memoryObject struct {
	content     []byte
	contentType string
	updatedAt   time.Time
}

func newMemoryObjectStore() *memoryObjectStore {
	return &memoryObjectStore{objects: make(map[string]memoryObject)}
}

func (s *memoryObjectStore) put(_ context.Context, key string, content []byte, contentType string) error {
	s.mu.Lock()
	s.objects[key] = memoryObject{content: append([]byte(nil), content...), contentType: contentType, updatedAt: time.Now().UTC()}
	s.mu.Unlock()
	return nil
}

func (s *memoryObjectStore) get(_ context.Context, key string) ([]byte, objectInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	object, ok := s.objects[key]
	if !ok {
		return nil, objectInfo{}, errObjectNotFound
	}
	return append([]byte(nil), object.content...), objectInfo{Key: key, Size: int64(len(object.content)), ContentType: object.contentType, UpdatedAt: object.updatedAt}, nil
}

func (s *memoryObjectStore) list(_ context.Context, prefix string) ([]objectInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]objectInfo, 0)
	for key, object := range s.objects {
		if strings.HasPrefix(key, prefix) {
			result = append(result, objectInfo{Key: key, Size: int64(len(object.content)), ContentType: object.contentType, UpdatedAt: object.updatedAt})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, nil
}

func (s *memoryObjectStore) delete(_ context.Context, key string) error {
	s.mu.Lock()
	delete(s.objects, key)
	s.mu.Unlock()
	return nil
}

func (s *memoryObjectStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.objects)
}
