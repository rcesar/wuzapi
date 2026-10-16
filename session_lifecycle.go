package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"
)

const (
	sessionStopTimeout           = 15 * time.Second
	streamReplacementMaxRetries  = 5
	streamReplacementBaseBackoff = 2 * time.Second
	livenessCheckInterval        = 30 * time.Second
	livenessFailureThreshold     = 2 * time.Minute
	streamReplacementResetAfter  = 10 * time.Minute
)

var errSessionAlreadyOwned = errors.New("session is owned by another wuzapi process")

type sessionControl struct {
	userID     string
	generation uint64

	stop    chan struct{}
	restart chan string
	done    chan struct{}
	kill    chan bool

	stopOnce sync.Once
	doneOnce sync.Once
}

func newSessionControl(userID string, generation uint64) *sessionControl {
	return &sessionControl{
		userID:     userID,
		generation: generation,
		stop:       make(chan struct{}),
		restart:    make(chan string, 1),
		done:       make(chan struct{}),
		kill:       make(chan bool, 1),
	}
}

func (sc *sessionControl) requestStop() {
	if sc == nil {
		return
	}
	sc.stopOnce.Do(func() {
		close(sc.stop)
		select {
		case sc.kill <- true:
		default:
		}
	})
}

func (sc *sessionControl) requestRestart(reason string) {
	if sc == nil {
		return
	}
	select {
	case <-sc.stop:
		return
	default:
	}
	select {
	case sc.restart <- reason:
	default:
	}
}

func (sc *sessionControl) closeDone() {
	if sc == nil {
		return
	}
	sc.doneOnce.Do(func() { close(sc.done) })
}

var sessionRegistry = struct {
	sync.Mutex
	current        map[string]*sessionControl
	nextGeneration uint64
}{
	current: make(map[string]*sessionControl),
}

func currentSession(userID string) *sessionControl {
	sessionRegistry.Lock()
	sc := sessionRegistry.current[userID]
	sessionRegistry.Unlock()
	return sc
}

func sessionGeneration(sc *sessionControl) uint64 {
	if sc == nil {
		return 0
	}
	return sc.generation
}

func reserveSession(userID string) (*sessionControl, bool) {
	sessionRegistry.Lock()
	defer sessionRegistry.Unlock()
	if _, exists := sessionRegistry.current[userID]; exists {
		return nil, false
	}
	sessionRegistry.nextGeneration++
	sc := newSessionControl(userID, sessionRegistry.nextGeneration)
	sessionRegistry.current[userID] = sc
	return sc, true
}

func releaseSession(sc *sessionControl) {
	if sc == nil {
		return
	}
	sessionRegistry.Lock()
	if current := sessionRegistry.current[sc.userID]; current == sc {
		delete(sessionRegistry.current, sc.userID)
	}
	sessionRegistry.Unlock()
	sc.closeDone()
}

func stopAndWaitForSession(userID string, timeout time.Duration) error {
	sc := currentSession(userID)
	if sc == nil {
		return nil
	}

	log.Info().Str("userID", userID).Uint64("generation", sc.generation).Msg("Stopping previous session before creating a new client")
	sc.requestStop()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-sc.done:
		return nil
	case <-timer.C:
		return fmt.Errorf("timed out waiting for session generation %d to stop", sc.generation)
	}
}

func waitForStopOrDuration(sc *sessionControl, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-sc.stop:
		return true
	case <-timer.C:
		return false
	}
}

func (mycli *MyClient) startLivenessWatchdog(ctx context.Context) {
	ticker := time.NewTicker(livenessCheckInterval)
	defer ticker.Stop()

	var disconnectedSince time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if mycli.WAClient.IsConnected() || !mycli.WAClient.IsLoggedIn() {
			disconnectedSince = time.Time{}
			continue
		}

		if disconnectedSince.IsZero() {
			disconnectedSince = time.Now()
			continue
		}
		if time.Since(disconnectedSince) < livenessFailureThreshold {
			continue
		}

		lastConnected := time.Unix(0, mycli.lastConnected.Load())
		lastKeepAlive := time.Unix(0, mycli.lastKeepAlive.Load())
		log.Warn().
			Str("userID", mycli.userID).
			Uint64("generation", sessionGeneration(mycli.session)).
			Time("lastConnected", lastConnected).
			Time("lastKeepAlive", lastKeepAlive).
			Dur("disconnectedFor", time.Since(disconnectedSince)).
			Msg("Session liveness check failed; scheduling client replacement")
		mycli.scheduleSessionReplacement("liveness")
		return
	}
}

type sessionLease struct {
	conn *sql.Conn
	key  string
}

// acquireSessionLease prevents two WuzAPI processes sharing the same
// WhatsApp device store when the application is backed by PostgreSQL. SQLite
// deployments must run a single WuzAPI replica because it has no equivalent
// per-session advisory lock.
func acquireSessionLease(ctx context.Context, db *sqlx.DB, userID string) (*sessionLease, error) {
	if db == nil || db.DriverName() != "postgres" {
		return nil, nil
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("open session lease connection: %w", err)
	}

	key := "wuzapi:whatsapp-session:" + userID
	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(hashtext($1))", key).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("acquire session advisory lock: %w", err)
	}
	if !acquired {
		_ = conn.Close()
		return nil, errSessionAlreadyOwned
	}

	return &sessionLease{conn: conn, key: key}, nil
}

func (lease *sessionLease) release(ctx context.Context) {
	if lease == nil || lease.conn == nil {
		return
	}
	var released bool
	if err := lease.conn.QueryRowContext(ctx, "SELECT pg_advisory_unlock(hashtext($1))", lease.key).Scan(&released); err != nil {
		log.Warn().Err(err).Msg("Failed to release session advisory lock")
	} else if !released {
		log.Warn().Str("key", lease.key).Msg("Session advisory lock was not held during release")
	}
	if err := lease.conn.Close(); err != nil {
		log.Warn().Err(err).Msg("Failed to close session lease connection")
	}
}

// nextReplacementCount returns the restart counter after an attempt that ran for
// uptime. An attempt that stayed up long enough is not part of a rapid loop, so
// the lifetime cap only applies to restarts in quick succession.
func nextReplacementCount(count int, uptime time.Duration) int {
	if uptime >= streamReplacementResetAfter {
		count = 0
	}
	return count + 1
}
