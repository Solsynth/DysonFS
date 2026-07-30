package service

import (
	"fmt"
	"testing"
)

func TestServerFailureLogRetainsNewestHundredAndKeepsCounters(t *testing.T) {
	log := NewServerFailureLog()
	for i := 0; i < MaxServerFailureEvents+1; i++ {
		log.Record(ServerFailureEvent{Path: fmt.Sprintf("/failure/%d", i), UploadFailure: i%2 == 0})
	}

	snapshot := log.Snapshot(MaxServerFailureEvents)
	if snapshot.RetainedEventCount != MaxServerFailureEvents {
		t.Fatalf("retained events = %d, want %d", snapshot.RetainedEventCount, MaxServerFailureEvents)
	}
	if snapshot.ServerFailureCount != MaxServerFailureEvents+1 {
		t.Fatalf("server failures = %d, want %d", snapshot.ServerFailureCount, MaxServerFailureEvents+1)
	}
	if snapshot.UploadFailureCount != 51 {
		t.Fatalf("upload failures = %d, want 51", snapshot.UploadFailureCount)
	}
	if len(snapshot.Events) != MaxServerFailureEvents || snapshot.Events[0].Path != "/failure/100" || snapshot.Events[len(snapshot.Events)-1].Path != "/failure/1" {
		t.Fatalf("events do not retain newest failures: %+v", snapshot.Events)
	}
}
