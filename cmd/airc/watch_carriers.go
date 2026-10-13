package main

import (
	"context"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/fallback"
	"github.com/Someblueman/airc/pkg/irc"
)

// roomPage is one watch page for one room.
type roomPage struct {
	room string
	page *fallback.WatchPage
}

// followRooms watches each room over fb's gateway carriers from its start (a
// message ID, or "" for the latest), reconnecting with backoff, and delivers
// the pages until ctx ends; the returned wait waits for the watches to stop.
func followRooms(ctx context.Context, fb *fallback.Client, starts map[string]string) (<-chan roomPage, func()) {
	pages := make(chan roomPage)
	var wg sync.WaitGroup
	for room, after := range starts {
		wg.Go(func() {
			retry := newBackoff(250*time.Millisecond, 5*time.Second, true)
			for ctx.Err() == nil {
				watch, err := fb.WatchRoom(ctx, room, after, 100)
				if err == nil {
					retry.reset()
					select { // connected: an empty page names the carrier at once
					case pages <- roomPage{room, &fallback.WatchPage{Endpoint: watch.Endpoint()}}:
					case <-ctx.Done():
					}
					for {
						page, err := watch.Next(ctx)
						if err != nil {
							break
						}
						select {
						case pages <- roomPage{room, page}:
						case <-ctx.Done():
						}
					}
					after = watch.Last()
					_ = watch.Close()
				}
				if !retry.wait(ctx) {
					return
				}
			}
		})
	}
	return pages, wg.Wait
}

// bridge follows the watched rooms over the server's other carriers while
// native IRC is unreachable, from the last message shown in each, so the
// watcher misses nothing and shows nothing twice. Direct messages wait for
// native IRC, whose session then catches them up.
func (w *watcher) bridge(ctx context.Context) error {
	starts := map[string]string{}
	dms := false
	for _, target := range w.targets {
		if protocol.IsChannel(target.history) {
			starts[target.history] = w.last[targetKey(target.history)]
		} else {
			dms = true
		}
	}
	if len(starts) == 0 {
		return nil
	}
	fb, _, err := racingClient(w.opt, preferLast)
	if err != nil || fb == nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	pages, wait := followRooms(ctx, fb, starts)
	defer wait()
	defer cancel()
	announced := ""
	for {
		select {
		case <-ctx.Done():
			return nil
		case d := <-pages:
			if carrier := d.page.Endpoint.String(); carrier != announced {
				announced = carrier
				if err := w.continuing(carrier, dms); err != nil {
					return err
				}
			}
			if d.page.Gap {
				if err := w.show("some messages in "+d.room+" from while native IRC was unreachable are no longer available", false); err != nil {
					return err
				}
			}
			for _, m := range d.page.Messages {
				if err := w.emit(historyEvent(m).MessageEvent()); err != nil {
					return err
				}
			}
		}
	}
}

// continuing reports that the watch goes on over another carrier.
func (w *watcher) continuing(carrier string, dms bool) error {
	if w.json {
		return w.encoder.Encode(&irc.ConnectionEvent{Type: "connection", Connected: true, Carrier: carrier})
	}
	return w.write(w.view.info(continuingText(carrier, dms), true))
}

func continuingText(carrier string, dms bool) string {
	text := "following rooms over " + carrier + " while native IRC is unreachable"
	if dms {
		text += "; direct messages follow when it returns"
	}
	return text
}
