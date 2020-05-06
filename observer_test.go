package leaderelection

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vimeo/leaderelection/clocks"
	"github.com/vimeo/leaderelection/entry"
	"github.com/vimeo/leaderelection/memory"
)

type atomicBool struct {
	i uint32
}

func (a *atomicBool) set(b bool) {
	v := uint32(0)
	if b {
		v = 1
	}
	atomic.StoreUint32(&a.i, v)
}
func (a *atomicBool) get() bool {
	return atomic.LoadUint32(&a.i) > 0
}

func TestObserverWithMultipleContendersAndFakeClock(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := memory.NewDecider()
	elected := atomicBool{}
	onElectedCalls := 0
	wg := sync.WaitGroup{}
	defer wg.Wait()
	fc := clocks.NewFakeClock(time.Now())

	watchConfig := WatchConfig{
		Decider: d,
		Clock:   fc,
	}

	watchCBs := 0
	wg.Add(1)
	go func() {
		defer wg.Done()
		watchErr := watchConfig.Watch(ctx, func(ctx context.Context, e entry.RaceEntry) {
			// we only want to see this once per election
			if int(e.ElectionNumber) != watchCBs-1 {
				t.Errorf("unexpected election number: %d; watchCBs %d", e.ElectionNumber, watchCBs)
			}
			watchCBs++
		})
		if watchErr != nil && watchErr != context.Canceled {
			t.Errorf("watch failed: %s", watchErr)
		}
	}()

	// wait until the Watcher goes to sleep waiting for the next time it needs to awaken.
	fc.AwaitSleepers(1)
	if sleepers := fc.NumSleepers(); sleepers != 1 {
		t.Errorf("unexpected number of goroutines sleeping; want %d; got %d", 1, sleepers)
	}

	loserConfig := Config{
		OnElected:        func(context.Context, *TimeView) { t.Error("unexpected election win of loser") },
		LeaderChanged:    func(_ context.Context, e entry.RaceEntry) { t.Logf("lost election: %+v", e) },
		LeaderID:         "nottheleader",
		HostPort:         "127.0.0.2:7123",
		Decider:          d,
		TermLength:       time.Minute * 30,
		ConnectionParams: []byte("bimbat"),
		MaxClockSkew:     time.Second,
		Clock:            fc,
	}
	const realleaderID = "yabadabadoo"
	const realLeaderConnectionParams = "boodle_doodle"

	c := Config{
		Decider:  d,
		HostPort: "127.0.0.1:8080",
		LeaderID: realleaderID,
		OnElected: func(ctx context.Context, tv *TimeView) {
			if elected.get() {
				t.Error("OnElected called while still elected")
			}
			elected.set(true)
			onElectedCalls++
		},
		OnOusting: func(ctx context.Context) {
			elected.set(false)
		},
		LeaderChanged: func(ctx context.Context, rentry entry.RaceEntry) {
			if rentry.LeaderID != realleaderID && rentry.ElectionNumber != entry.NoElections {
				t.Errorf("wrong leader grabbed lock: %q", rentry.LeaderID)
			}
			if string(rentry.ConnectionParams) != realLeaderConnectionParams && rentry.ElectionNumber != entry.NoElections {
				t.Errorf("wrong ConnectionParams: %q; expected %q",
					string(rentry.ConnectionParams), realLeaderConnectionParams)
			}
		},
		TermLength:       time.Minute * 30,
		MaxClockSkew:     time.Second,
		ConnectionParams: []byte(realLeaderConnectionParams),
		Clock:            fc,
	}

	if sleepers := fc.NumSleepers(); sleepers != 1 {
		t.Errorf("unexpected number of goroutines sleeping; want %d; got %d", 0, sleepers)
	}

	acquireCh := make(chan error, 1)
	go func() {
		acquireCh <- c.Acquire(ctx)
	}()

	// wait until manageWin goes to sleep waiting for the next time it needs to awaken.
	fc.AwaitSleepers(2)
	if sleepers := fc.NumSleepers(); sleepers != 2 {
		t.Errorf("unexpected number of goroutines sleeping; want %d; got %d", 1, sleepers)
	}
	// now that the winning candidate is sleeping after having won, spin
	// off another goroutine with a loser to contend on the lock.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := loserConfig.Acquire(ctx); err != context.Canceled {
			t.Errorf("non-cancel error: %s", err)
		}

	}()

	// Advance from our initial timestamp up to half-way into the term.
	// (MaxClockSkew past the point we're expecting to wake-up)
	// This should wake up our sleeping goroutine
	fc.Advance(c.TermLength / 2)

	// wait for manageWin to go back to sleep again, and then cancel the context
	fc.AwaitSleepers(3)
	if sleepers := fc.NumSleepers(); sleepers != 3 {
		t.Errorf("unexpected number of goroutines sleeping; want %d; got %d", 2, sleepers)
	}
	cancel()
	if err := <-acquireCh; err != context.Canceled {
		t.Errorf("failed to acquire: %s", err)
	}

	if onElectedCalls != 1 {
		t.Errorf("unexpected number of OnElected calls: %d", onElectedCalls)
	}

	wg.Wait()
}

func ExampleWatchConfig_Watch() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := memory.NewDecider()
	now := time.Date(2020, 5, 6, 0, 0, 0, 0, time.UTC)
	fc := clocks.NewFakeClock(now)

	watchConfig := WatchConfig{
		Decider: d,
		Clock:   fc,
	}

	c, _ := d.ReadCurrent(ctx)

	d.WriteEntry(ctx, &entry.RaceEntry{
		LeaderID:         "fimbat",
		HostPort:         "test:80",
		TermExpiry:       now.Add(time.Hour),
		ElectionNumber:   c.ElectionNumber + 1,
		ConnectionParams: nil,
		Token:            c.Token,
	})

	ch := make(chan struct{})
	go func() {
		defer close(ch)
		_ = watchConfig.Watch(ctx, func(ctx context.Context, e entry.RaceEntry) {
			fmt.Println(e.HostPort, e.TermExpiry)
		})
	}()

	fc.AwaitSleepers(1)
	cancel()
	<-ch

	// Output:
	// test:80 2020-05-06 01:00:00 +0000 UTC
}
