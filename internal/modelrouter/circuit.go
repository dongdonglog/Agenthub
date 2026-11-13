package modelrouter

import (
	"sync"
	"time"
)

type circuit struct {
	mu        sync.Mutex
	failures  int
	openUntil time.Time
	probing   bool
	lastError string
	lastMS    int64
}

func (c *circuit) allow(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Before(c.openUntil) {
		return false
	}
	if !c.openUntil.IsZero() && now.After(c.openUntil) {
		if c.probing {
			return false
		}
		c.probing = true
	}
	return true
}

func (c *circuit) success(latencyMS int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures = 0
	c.openUntil = time.Time{}
	c.probing = false
	c.lastError = ""
	c.lastMS = latencyMS
}

func (c *circuit) failure(threshold int, cooldown time.Duration, message string, latencyMS int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures++
	c.lastError = message
	c.lastMS = latencyMS
	c.probing = false
	if c.failures >= threshold {
		c.openUntil = time.Now().Add(cooldown)
	}
}
