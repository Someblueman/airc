package store

import "slices"

// watch registers a channel that receives the job's next saved outcome. The
// caller holds s.mu, so an outcome cannot be saved between its admission's
// commit and this registration.
func (s *Store) watch(key string) chan Job {
	c := make(chan Job, 1)
	s.waitMu.Lock()
	defer s.waitMu.Unlock()
	if s.waiters == nil {
		s.waiters = map[string][]chan Job{}
	}
	s.waiters[key] = append(s.waiters[key], c)
	return c
}

// unwatch drops a channel that no longer waits. It needs only waitMu, so a
// timed-out admission never queues behind the store lock.
func (s *Store) unwatch(key string, c chan Job) {
	s.waitMu.Lock()
	defer s.waitMu.Unlock()
	list := slices.DeleteFunc(s.waiters[key], func(w chan Job) bool { return w == c })
	if len(list) == 0 {
		delete(s.waiters, key)
	} else {
		s.waiters[key] = list
	}
}

// saved hands a committed outcome to the admissions waiting for it. Each
// channel is buffered and receives once, so this never blocks.
func (s *Store) saved(j Job) {
	key := secretKey(j.Scope, j.Request.RequestID)
	s.waitMu.Lock()
	defer s.waitMu.Unlock()
	for _, c := range s.waiters[key] {
		c <- j
	}
	delete(s.waiters, key)
}
