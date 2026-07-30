package service

import (
	"sync"
	"time"
)

const MaxServerFailureEvents = 100

// ServerFailureEvent is a bounded, in-memory record of an HTTP server error.
// It deliberately stores no query string or request body, which may contain
// credentials or uploaded file content.
type ServerFailureEvent struct {
	OccurredAt    time.Time `json:"occurred_at"`
	Method        string    `json:"method"`
	Path          string    `json:"path"`
	Status        int       `json:"status"`
	Detail        string    `json:"detail,omitempty"`
	UploadFailure bool      `json:"upload_failure"`
}

type ServerFailureLogSnapshot struct {
	Capacity           int                  `json:"capacity"`
	RetainedEventCount int                  `json:"retained_event_count"`
	ServerFailureCount uint64               `json:"server_failure_count"`
	UploadFailureCount uint64               `json:"upload_failure_count"`
	Events             []ServerFailureEvent `json:"events"`
}

// ServerFailureLog retains the newest 100 server errors while maintaining
// process-lifetime counters even after older event details are evicted.
type ServerFailureLog struct {
	mu                 sync.RWMutex
	events             []ServerFailureEvent
	serverFailureCount uint64
	uploadFailureCount uint64
}

func NewServerFailureLog() *ServerFailureLog {
	return &ServerFailureLog{events: make([]ServerFailureEvent, 0, MaxServerFailureEvents)}
}

func (l *ServerFailureLog) Record(event ServerFailureEvent) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.serverFailureCount++
	if event.UploadFailure {
		l.uploadFailureCount++
	}
	if len(l.events) == MaxServerFailureEvents {
		copy(l.events, l.events[1:])
		l.events[len(l.events)-1] = event
		return
	}
	l.events = append(l.events, event)
}

func (l *ServerFailureLog) Snapshot(limit int) ServerFailureLogSnapshot {
	if l == nil {
		return ServerFailureLogSnapshot{Capacity: MaxServerFailureEvents, Events: []ServerFailureEvent{}}
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if limit <= 0 || limit > len(l.events) {
		limit = len(l.events)
	}
	events := make([]ServerFailureEvent, limit)
	for i := 0; i < limit; i++ {
		events[i] = l.events[len(l.events)-1-i]
	}
	return ServerFailureLogSnapshot{
		Capacity:           MaxServerFailureEvents,
		RetainedEventCount: len(l.events),
		ServerFailureCount: l.serverFailureCount,
		UploadFailureCount: l.uploadFailureCount,
		Events:             events,
	}
}
