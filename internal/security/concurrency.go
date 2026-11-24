package security

import "sync"

type Concurrency struct {
	mu     sync.Mutex
	active map[string]int
	max    int
}

func NewConcurrency(max int) *Concurrency {
	if max < 1 {
		max = 1
	}
	return &Concurrency{active: map[string]int{}, max: max}
}

func (c *Concurrency) Acquire(identity string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active[identity] >= c.max {
		return false
	}
	c.active[identity]++
	return true
}

func (c *Concurrency) Release(identity string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active[identity] <= 1 {
		delete(c.active, identity)
		return
	}
	c.active[identity]--
}
