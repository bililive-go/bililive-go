package simplecache

import (
	"errors"
	"sync"
)

var ErrKeyNotFound = errors.New("key not found")

type Cache interface {
	Get(key interface{}) (interface{}, error)
	Set(key, value interface{}) error
}

type SimpleCache struct {
	mu    sync.RWMutex
	store map[interface{}]interface{}
}

func New() Cache {
	return &SimpleCache{
		store: make(map[interface{}]interface{}),
	}
}

func (c *SimpleCache) Get(key interface{}) (interface{}, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.store[key]
	if !ok {
		return nil, ErrKeyNotFound
	}
	return val, nil
}

func (c *SimpleCache) Set(key, value interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[key] = value
	return nil
}
