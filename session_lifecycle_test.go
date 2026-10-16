package main

import (
	"sync"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"go.mau.fi/whatsmeow"
)

func resetSessionRegistryForTest(t *testing.T) {
	t.Helper()
	sessionRegistry.Lock()
	sessionRegistry.current = make(map[string]*sessionControl)
	sessionRegistry.nextGeneration = 0
	sessionRegistry.Unlock()
}

func TestReserveSessionPreventsDuplicateClients(t *testing.T) {
	resetSessionRegistryForTest(t)

	first, ok := reserveSession("session-owner")
	if !ok {
		t.Fatal("first session reservation failed")
	}
	defer releaseSession(first)

	if second, ok := reserveSession("session-owner"); ok || second != nil {
		t.Fatal("second session reservation succeeded while the first session was active")
	}
	if got := currentSession("session-owner"); got != first {
		t.Fatal("currentSession returned a different session control")
	}
}

func TestConcurrentReserveSessionHasSingleOwner(t *testing.T) {
	resetSessionRegistryForTest(t)

	const attempts = 32
	var wg sync.WaitGroup
	owners := make(chan *sessionControl, attempts)
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			if session, ok := reserveSession("concurrent-session"); ok {
				owners <- session
			}
		}()
	}
	wg.Wait()
	close(owners)

	var owner *sessionControl
	for session := range owners {
		if owner != nil {
			t.Fatal("more than one concurrent session reservation succeeded")
		}
		owner = session
	}
	if owner == nil {
		t.Fatal("no concurrent session reservation succeeded")
	}
	releaseSession(owner)
}

func TestRestartDoesNotAllowConcurrentReservation(t *testing.T) {
	resetSessionRegistryForTest(t)

	session, ok := reserveSession("autoreconnect-session")
	if !ok {
		t.Fatal("session reservation failed")
	}
	session.requestRestart("stream_replaced")
	if replacement, ok := reserveSession("autoreconnect-session"); ok || replacement != nil {
		t.Fatal("a replacement session was reserved before the old session stopped")
	}
	releaseSession(session)
}

func TestScheduleSessionReplacementRequestsRestart(t *testing.T) {
	resetSessionRegistryForTest(t)

	session, ok := reserveSession("stream-error-session")
	if !ok {
		t.Fatal("session reservation failed")
	}
	defer releaseSession(session)

	mycli := &MyClient{userID: "stream-error-session", session: session}
	mycli.scheduleSessionReplacement("stream_error")

	select {
	case reason := <-session.restart:
		if reason != "stream_error" {
			t.Fatalf("restart reason = %q, want stream_error", reason)
		}
	default:
		t.Fatal("session replacement was not requested")
	}
}

func TestStopAndWaitForSessionWaitsForOwner(t *testing.T) {
	resetSessionRegistryForTest(t)

	sc, ok := reserveSession("session-stop")
	if !ok {
		t.Fatal("session reservation failed")
	}
	go func() {
		<-sc.stop
		releaseSession(sc)
	}()

	if err := stopAndWaitForSession("session-stop", time.Second); err != nil {
		t.Fatalf("stopAndWaitForSession returned error: %v", err)
	}
	if got := currentSession("session-stop"); got != nil {
		t.Fatal("session remained registered after the owner stopped")
	}
}

func TestSessionGenerationChangesAfterRelease(t *testing.T) {
	resetSessionRegistryForTest(t)

	first, ok := reserveSession("session-generation")
	if !ok {
		t.Fatal("first session reservation failed")
	}
	releaseSession(first)

	second, ok := reserveSession("session-generation")
	if !ok {
		t.Fatal("second session reservation failed")
	}
	defer releaseSession(second)
	if second.generation <= first.generation {
		t.Fatalf("second generation = %d, first generation = %d", second.generation, first.generation)
	}
}

func TestClientManagerConditionalCleanupPreservesNewSession(t *testing.T) {
	manager := NewClientManager()
	userID := "conditional-cleanup"

	oldClient := &whatsmeow.Client{}
	newClient := &whatsmeow.Client{}
	manager.SetWhatsmeowClient(userID, newClient)
	if manager.DeleteWhatsmeowClientIf(userID, oldClient) {
		t.Fatal("stale client cleanup removed the current WhatsApp client")
	}
	if manager.GetWhatsmeowClient(userID) != newClient {
		t.Fatal("current WhatsApp client was not preserved")
	}

	oldHTTP := resty.New()
	newHTTP := resty.New()
	manager.SetHTTPClient(userID, newHTTP)
	if manager.DeleteHTTPClientIf(userID, oldHTTP) {
		t.Fatal("stale client cleanup removed the current HTTP client")
	}
	if manager.GetHTTPClient(userID) != newHTTP {
		t.Fatal("current HTTP client was not preserved")
	}

	oldMyClient := &MyClient{}
	newMyClient := &MyClient{}
	manager.SetMyClient(userID, newMyClient)
	if manager.DeleteMyClientIf(userID, oldMyClient) {
		t.Fatal("stale client cleanup removed the current MyClient")
	}
	if manager.GetMyClient(userID) != newMyClient {
		t.Fatal("current MyClient was not preserved")
	}
}
