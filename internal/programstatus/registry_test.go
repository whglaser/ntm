package programstatus

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestRegistryLatestWinsPerPane(t *testing.T) {
	registry := NewRegistry()
	registry.Apply(Event{PaneID: "%0", State: StateWorking, App: "pi"})
	registry.Apply(Event{PaneID: "%1", State: StateIdle, App: "pi"})
	registry.Apply(Event{PaneID: "%0", State: StateDone, App: "pi"})

	event, ok := registry.Latest("%0")
	if !ok || event.State != StateDone {
		t.Fatalf("Latest(%%0) = %+v, %v; want done", event, ok)
	}
	if event, ok := registry.Latest("%1"); !ok || event.State != StateIdle {
		t.Fatalf("Latest(%%1) = %+v, %v; want idle", event, ok)
	}
	if _, ok := registry.Latest("%2"); ok {
		t.Fatal("Latest(%%2) unexpectedly present")
	}
	if got := registry.Len(); got != 2 {
		t.Fatalf("Len() = %d, want 2", got)
	}
}

func TestRegistryClearRemovesPane(t *testing.T) {
	registry := NewRegistry()
	registry.Apply(Event{PaneID: "%0", State: StateWorking})
	registry.Apply(Event{PaneID: "%0", State: StateClear})

	if event, ok := registry.Latest("%0"); ok {
		t.Fatalf("Latest after clear = %+v, want absent", event)
	}
	if got := registry.Len(); got != 0 {
		t.Fatalf("Len() = %d after clear, want 0", got)
	}
}

func TestRegistryIgnoresQueriesAndEmptyPanes(t *testing.T) {
	registry := NewRegistry()
	registry.Apply(Event{PaneID: "%0", Query: true})
	registry.Apply(Event{PaneID: "", State: StateWorking})
	if got := registry.Len(); got != 0 {
		t.Fatalf("Len() = %d, want 0", got)
	}
}

func TestRegistrySnapshotIsACopy(t *testing.T) {
	registry := NewRegistry()
	registry.Apply(Event{PaneID: "%0", State: StateWorking})
	snapshot := registry.Snapshot()
	snapshot["%0"] = Event{PaneID: "%0", State: StateDone}

	if event, _ := registry.Latest("%0"); event.State != StateWorking {
		t.Fatalf("snapshot mutation leaked into registry: %+v", event)
	}
}

func TestRegistryConcurrentApplyAndLatest(t *testing.T) {
	registry := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			pane := fmt.Sprintf("%%%d", worker%4)
			for n := 0; n < 200; n++ {
				registry.Apply(Event{PaneID: pane, State: StateWorking})
				registry.Latest(pane)
			}
		}(i)
	}
	wg.Wait()
	if got := registry.Len(); got != 4 {
		t.Fatalf("Len() = %d, want 4", got)
	}
}

func TestObserverRoutesEventsPerPaneAcrossChunks(t *testing.T) {
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	observer := NewObserver()
	observer.now = func() time.Time { return at }

	chunks := [][]byte{
		[]byte("\x1b]7501;state=work"),
		[]byte("ing:app=pi\x1b\\"),
		[]byte(esc + "]7501;state=blocked:app=pi:kind=trust" + belStr),
	}

	var events []Event
	for _, chunk := range chunks {
		events = append(events, observer.Observe("%0", chunk)...)
	}
	if got := states(events); len(got) != 2 || got[0] != StateWorking || got[1] != StateBlocked {
		t.Fatalf("states = %v, want [working blocked]", got)
	}

	latest, ok := observer.Latest("%0")
	if !ok || latest.State != StateBlocked || latest.Kind != "trust" {
		t.Fatalf("Latest(%%0) = %+v, %v; want blocked trust", latest, ok)
	}
	if _, ok := observer.Latest("%1"); ok {
		t.Fatal("pane %%1 should have no report")
	}
}

func TestObserverForgetDropsParserAndReport(t *testing.T) {
	observer := NewObserver()
	observer.Observe("%0", []byte("\x1b]7501;state=work"))
	observer.Forget("%0")

	if _, ok := observer.Latest("%0"); ok {
		t.Fatal("report survived Forget")
	}
	// A fresh parser must not see the predecessor's half sequence.
	observer.Observe("%0", []byte("ing\x1b\\"))
	if _, ok := observer.Latest("%0"); ok {
		t.Fatal("half sequence from predecessor completed after Forget")
	}
}

func TestObserverSatisfiesSingleMethodSource(t *testing.T) {
	type source interface {
		Latest(paneID string) (Event, bool)
	}
	var _ source = NewObserver()
}
