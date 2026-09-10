package streams

import (
	"fmt"
	"maps"
	"net/url"
	"sync"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/probe"
)

type Preload struct {
	stream *Stream      // Don't include the stream in JSON to avoid leaking secrets.
	Cons   *probe.Probe `json:"consumer"`
	Query  string       `json:"query"`
}

var preloads = map[string]*Preload{}
var preloadsMu sync.Mutex

// preloadRetries counts scheduled retries per stream name. It doubles as a
// cancellation token: a pending retry only runs if its attempt number is
// still the current one, so a retry scheduled before DelPreload cannot
// resurrect a preload the user removed. Guarded by preloadsMu.
var preloadRetries = map[string]int{}

func AddPreload(name, rawQuery string) error {
	if rawQuery == "" {
		rawQuery = "video&audio"
	}

	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return err
	}

	preloadsMu.Lock()
	defer preloadsMu.Unlock()

	if p := preloads[name]; p != nil {
		p.stream.RemoveConsumer(p.Cons)
	}

	stream := Get(name)
	if stream == nil {
		return fmt.Errorf("streams: stream not found: %s", name)
	}
	cons := probe.Create("preload", query)

	if err = stream.AddConsumer(cons); err != nil {
		// Retry instead of dropping the preload. AddConsumer dials the
		// producer, so a source that is not ready yet at startup, or a
		// camera that is briefly refusing connections, permanently loses
		// its preload even though the same dial would succeed seconds
		// later. The stream then stays down until someone restarts
		// go2rtc or re-adds the preload by hand.
		schedulePreloadRetry(name, rawQuery)
		return err
	}

	delete(preloadRetries, name)
	preloads[name] = &Preload{stream: stream, Cons: cons, Query: rawQuery}
	return nil
}

// schedulePreloadRetry queues a deferred AddPreload(name, rawQuery) call.
// The backoff ladder matches the producer reconnect ladder so the two retry
// loops do not fight each other: 5s for the first few attempts, then 15s,
// then once a minute for a sustained outage. Caller must hold preloadsMu.
func schedulePreloadRetry(name, rawQuery string) {
	preloadRetries[name]++
	attempt := preloadRetries[name]

	delay := time.Minute
	switch {
	case attempt < 5:
		delay = 5 * time.Second
	case attempt < 10:
		delay = 15 * time.Second
	}

	log.Warn().Str("name", name).Int("attempt", attempt).Dur("retry_in", delay).Msg(
		"[streams] preload AddConsumer failed; will retry",
	)

	time.AfterFunc(delay, func() {
		// Skip if the preload was removed, or re-added and rescheduled,
		// while this retry was pending.
		preloadsMu.Lock()
		stale := preloadRetries[name] != attempt
		preloadsMu.Unlock()

		if stale {
			return
		}

		if err := AddPreload(name, rawQuery); err != nil {
			log.Trace().Err(err).Str("name", name).Msg(
				"[streams] preload retry still failing",
			)
		}
	})
}

func DelPreload(name string) error {
	preloadsMu.Lock()
	defer preloadsMu.Unlock()

	// Cancel any pending retry: the scheduled closure checks its attempt
	// number against this map and gives up when it no longer matches.
	delete(preloadRetries, name)

	if p := preloads[name]; p != nil {
		p.stream.RemoveConsumer(p.Cons)
		delete(preloads, name)
		return nil
	}

	return fmt.Errorf("streams: preload not found: %s", name)
}

func GetPreloads() map[string]*Preload {
	preloadsMu.Lock()
	defer preloadsMu.Unlock()
	return maps.Clone(preloads)
}
