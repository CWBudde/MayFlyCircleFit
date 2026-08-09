package server

import (
	"sync"
	"testing"
	"time"
)

func TestEventBroadcasterConcurrentLifecycle(t *testing.T) {
	eb := NewEventBroadcaster()
	const jobID = "2b35aa54-6343-4d6e-86c1-915bb5543430"

	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func(iteration int) {
			defer wg.Done()
			ch := eb.Subscribe(jobID)
			eb.Broadcast(ProgressEvent{
				JobID:      jobID,
				State:      StateRunning,
				Iterations: iteration,
				Timestamp:  time.Now(),
			})
			eb.Unsubscribe(jobID, ch)
		}(i)
	}
	wg.Wait()

	eb.CleanupJob(jobID)
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	if _, ok := eb.clients[jobID]; ok {
		t.Fatal("client cache was not removed")
	}
	if _, ok := eb.lastEvent[jobID]; ok {
		t.Fatal("last-event cache was not removed")
	}
}

func TestEventBroadcasterCleanupMakesUnsubscribeIdempotent(t *testing.T) {
	eb := NewEventBroadcaster()
	const jobID = "b65ef8ca-150c-4f74-ae59-661140af049f"
	ch := eb.Subscribe(jobID)

	eb.CleanupJob(jobID)
	eb.Unsubscribe(jobID, ch)
}
