package leaderelection

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/vimeo/go-clocks/fake"
	"github.com/vimeo/go-clocks/offset"

	"github.com/vimeo/leaderelection/entry"
	"github.com/vimeo/leaderelection/memory"
)

func init() {
	// Seed the PRNG with something mostly run-specific (used for jitter)
	rand.Seed(int64(os.Getpid()) * time.Now().UnixNano())
}

func TestAcquireTrivialUncontendedFakeClock(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := memory.NewDecider()
	elected := atomicBool{}
	onElectedCalls := 0

	fc := fake.NewClock(time.Now())

	c := Config{
		Decider:  d,
		HostPort: []string{"127.0.0.1:8080"},
		LeaderID: "yabadabadoo",
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
		LeaderChanged: func(ctx context.Context, entry entry.RaceEntry) {
		},
		TermLength:   time.Minute * 30,
		MaxClockSkew: time.Second * 10,
		Clock:        fc,
	}

	if sleepers := fc.NumSleepers(); sleepers != 0 {
		t.Errorf("unexpected number of goroutines sleeping; want %d; got %d", 0, sleepers)
	}

	acquireCh := make(chan error, 1)
	go func() {
		acquireCh <- c.Acquire(ctx)
	}()

	// wait until manageWin goes to sleep waiting for the next time it needs to awaken.
	fc.AwaitSleepers(1)
	if sleepers := fc.NumSleepers(); sleepers != 1 {
		t.Errorf("unexpected number of goroutines sleeping; want %d; got %d", 1, sleepers)
	}

	// Advance from our initial timestamp up to half-way into the term.
	// (MaxClockSkew past the point we're expecting to wake-up)
	// This should wake up our sleeping goroutine
	fc.Advance(c.TermLength / 2)

	// wait for manageWin to go back to sleep again, and then cancel the context
	fc.AwaitSleepers(1)
	if sleepers := fc.NumSleepers(); sleepers != 1 {
		t.Errorf("unexpected number of goroutines sleeping; want %d; got %d", 1, sleepers)
	}
	cancel()
	if err := <-acquireCh; err != context.Canceled {
		t.Errorf("failed to acquire: %s", err)
	}

	if onElectedCalls != 1 {
		t.Errorf("unexpected number of OnElected calls: %d", onElectedCalls)
	}
}

func TestAcquireTrivialWithMinorContentionFakeClock(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := memory.NewDecider()
	elected := atomicBool{}
	onElectedCalls := 0
	wg := sync.WaitGroup{}
	defer wg.Wait()
	fc := fake.NewClock(time.Now())

	loserConfig := Config{
		OnElected:        func(context.Context, *TimeView) { t.Error("unexpected election win of loser") },
		LeaderChanged:    func(_ context.Context, e entry.RaceEntry) { t.Logf("lost election: %+v", e) },
		LeaderID:         "nottheleader",
		HostPort:         []string{"127.0.0.2:7123"},
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
		HostPort: []string{"127.0.0.1:8080"},
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

	if sleepers := fc.NumSleepers(); sleepers != 0 {
		t.Errorf("unexpected number of goroutines sleeping; want %d; got %d", 0, sleepers)
	}

	acquireCh := make(chan error, 1)
	go func() {
		acquireCh <- c.Acquire(ctx)
	}()

	// wait until manageWin goes to sleep waiting for the next time it needs to awaken.
	fc.AwaitSleepers(1)
	if sleepers := fc.NumSleepers(); sleepers != 1 {
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
	fc.AwaitSleepers(2)
	if sleepers := fc.NumSleepers(); sleepers != 2 {
		t.Errorf("unexpected number of goroutines sleeping; want %d; got %d", 2, sleepers)
	}
	cancel()
	if err := <-acquireCh; err != context.Canceled {
		t.Errorf("failed to acquire: %s", err)
	}

	if onElectedCalls != 1 {
		t.Errorf("unexpected number of OnElected calls: %d", onElectedCalls)
	}

}

func TestAcquireSkewedWithMinorContentionFakeClock(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := memory.NewDecider()
	elected := atomicBool{}
	onElectedCalls := 0
	wg := sync.WaitGroup{}
	defer wg.Wait()
	fc := fake.NewClock(time.Now())

	const termLen = time.Second * 10

	loserConfig := Config{
		OnElected:        func(context.Context, *TimeView) { t.Error("unexpected election win of loser") },
		LeaderChanged:    func(_ context.Context, e entry.RaceEntry) { t.Logf("lost election: %+v", e) },
		LeaderID:         "nottheleader",
		HostPort:         []string{"127.0.0.2:7123"},
		Decider:          d,
		TermLength:       termLen,
		ConnectionParams: []byte("bimbat"),
		MaxClockSkew:     time.Second,
		// Use a clock-skew that's barely within the MaxClockSkew range
		Clock: offset.NewOffsetClock(fc, -750*time.Millisecond),
	}
	const realleaderID = "yabadabadoo"
	const realLeaderConnectionParams = "boodle_doodle"

	c := Config{
		Decider:  d,
		HostPort: []string{"127.0.0.1:8080"},
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
		TermLength:       termLen,
		MaxClockSkew:     time.Second,
		ConnectionParams: []byte(realLeaderConnectionParams),
		Clock:            fc,
	}

	if sleepers := fc.NumSleepers(); sleepers != 0 {
		t.Errorf("unexpected number of goroutines sleeping; want %d; got %d", 0, sleepers)
	}

	acquireCh := make(chan error, 1)
	go func() {
		acquireCh <- c.Acquire(ctx)
	}()

	// wait until manageWin goes to sleep waiting for the next time it needs to awaken.
	fc.AwaitSleepers(1)
	if sleepers := fc.NumSleepers(); sleepers != 1 {
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

	// step forward 1000 times, making sure the loser never wins
	for l := 0; l < 1000; l++ {
		fc.Advance(time.Millisecond * 750)

		fc.AwaitSleepers(2)
		if sleepers := fc.NumSleepers(); sleepers != 2 {
			t.Errorf("unexpected number of goroutines sleeping; want %d; got %d", 2, sleepers)
		}
	}
	cancel()
	if err := <-acquireCh; err != context.Canceled {
		t.Errorf("failed to acquire: %s", err)
	}

	if onElectedCalls != 1 {
		t.Errorf("unexpected number of OnElected calls: %d", onElectedCalls)
	}

}

func TestAcquireAndReplaceWithFakeClockAndSkew(t *testing.T) {
	t.Parallel()
	wg := sync.WaitGroup{}
	defer wg.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := memory.NewDecider()
	firstElected := atomicBool{}
	firstElectedCh := make(chan struct{})
	secondElected := atomicBool{}
	secondElectedCh := make(chan struct{})
	onElectedCalls := 0
	baseTime := time.Now()
	fc := fake.NewClock(baseTime)

	const secondClockOffset = -time.Second
	secondWinnerClock := offset.NewOffsetClock(fc, secondClockOffset)

	firstTV := (*TimeView)(nil)
	firstCtx := context.Context(nil)
	secondTV := (*TimeView)(nil)
	secondCtx := context.Context(nil)

	secondWinnerConfig := Config{
		OnElected: func(ctx context.Context, tv *TimeView) {
			t.Logf("election win by second winner")
			secondTV = tv
			secondCtx = ctx
			secondElected.set(true)
			close(secondElectedCh)
		},
		LeaderChanged: func(_ context.Context, e entry.RaceEntry) { t.Logf("lost election: %+v", e) },
		OnOusting: func(ctx context.Context) {
			t.Logf("second candidate ousted (or shutdown)")
			select {
			case <-ctx.Done():
				return
			default:
				// this one shouldn't happen
				t.Errorf("second candidate ousted, not cancelled")
			}
		},
		LeaderID:         "nottheleaderYet",
		HostPort:         []string{"127.0.0.2:7123"},
		Decider:          d,
		TermLength:       time.Minute * 30,
		ConnectionParams: []byte("bimbat"),
		MaxClockSkew:     time.Second,
		Clock:            secondWinnerClock,
	}
	const realleaderID = "yabadabadoo"
	const realLeaderConnectionParams = "boodle_doodle"

	firstWinnerCtx, firstWinnerCancel := context.WithCancel(ctx)

	c := Config{
		Decider:  d,
		HostPort: []string{"127.0.0.1:8080"},
		LeaderID: realleaderID,
		OnElected: func(ctx context.Context, tv *TimeView) {
			if firstElected.get() {
				t.Error("OnElected called while still elected")
			}
			firstElected.set(true)
			onElectedCalls++
			firstTV = tv
			firstCtx = ctx
			close(firstElectedCh)
		},
		OnOusting: func(ctx context.Context) {
			firstElected.set(false)
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

	if sleepers := fc.NumSleepers(); sleepers != 0 {
		t.Errorf("unexpected number of goroutines sleeping; want %d; got %d", 0, sleepers)
	}

	acquireCh := make(chan error, 1)
	go func() {
		acquireCh <- c.Acquire(firstWinnerCtx)
	}()

	// wait until manageWin goes to sleep waiting for the next time it needs to awaken.
	fc.AwaitSleepers(1)
	if sleepers := fc.NumSleepers(); sleepers != 1 {
		t.Errorf("unexpected number of goroutines sleeping; want %d; got %d", 1, sleepers)
	}

	// wait for the first candidate's OnElected callback to complete
	<-firstElectedCh

	if firstTV == nil {
		t.Fatalf("first candidate's timeview is nil")
	}

	// Before we advance time, verify that we have the right timestamp returned by our TimeView
	if termEnd := firstTV.Get(); !termEnd.Equal(baseTime.Add(c.TermLength)) {
		t.Errorf("unexpected term end in TimeView: %s; expected %s",
			termEnd, baseTime.Add(c.TermLength))
	}

	// now that the winning candidate is sleeping after having won, spin
	// off another goroutine with a loser to contend on the lock.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := secondWinnerConfig.Acquire(ctx); err != context.Canceled {
			t.Errorf("non-cancel error: %s", err)
		}

	}()

	// make sure the secondWinner has actually gone to sleep
	fc.AwaitSleepers(2)

	// Advance from our initial timestamp up to half-way into the term.
	// (MaxClockSkew past the point we're expecting to wake-up)
	// This should wake up our sleeping goroutine
	fc.Advance(c.TermLength / 2)

	// wait for manageWin to go back to sleep again, and then cancel the context
	fc.AwaitSleepers(2)
	if sleepers := fc.NumSleepers(); sleepers != 2 {
		t.Errorf("unexpected number of goroutines sleeping; want %d; got %d", 2, sleepers)
	}

	if firstTV == nil {
		t.Fatalf("first candidate's timeview is nil")
	}
	select {
	case <-firstCtx.Done():
		t.Errorf("first candidate's callback context cancelled mid-term")
	default:
	}

	if termEnd := firstTV.Get(); !termEnd.Equal(baseTime.Add(c.TermLength + c.TermLength/2)) {
		t.Errorf("unexpected term end in TimeView: %s; expected %s",
			termEnd, baseTime.Add(c.TermLength+c.TermLength/2))
	}

	// Cancel the first leader, so the next one can take over once we get
	// to the end of the leader-election term.
	firstWinnerCancel()

	// wait for the first winner to exit.
	if err := <-acquireCh; err != context.Canceled {
		t.Errorf("failed to acquire: %s", err)
	}

	select {
	case <-firstCtx.Done():
	default:
		t.Errorf("first candidate's callback context not cancelled")
	}

	// Now, advance to just before the end of the term (according to the
	// remaining candidate)
	if awoken := fc.Advance(c.TermLength/2 + secondClockOffset - time.Microsecond); awoken != 0 {
		t.Errorf("too many sleepers awoken %d; wanted 0", awoken)
	}

	t.Logf("times: %v; now %v; first termExpiry %s", fc.Sleepers(), fc.Now(),
		baseTime.Add(c.TermLength+secondClockOffset))

	// now, advance all the way to the end of the term according to the
	// second candidate (the second candidate should still be sleeping
	// anyway)
	// no one should awaken since the original lock holder had its context cancelled.
	if awoken := fc.Advance(time.Microsecond); awoken != 0 {
		t.Errorf("too many sleepers awoken %d; wanted 0", awoken)
	}
	// In this test, the max clock-skew exactly matches the skew for our
	// second candidate, so we should still need another second in
	// fake-time before it thinks the term is up. However, we jitter the
	// return by 10%.
	if awoken := fc.Advance(secondWinnerConfig.MaxClockSkew); awoken != 0 {
		t.Errorf("too many sleepers awoken %d; wanted 0", awoken)
	}

	// now, advance 10% of the term-length so we're guaranteed that the
	// client has awoken.
	if awoken := fc.Advance(secondWinnerConfig.TermLength / 10); awoken != 1 {
		t.Errorf("unexpected number of sleepers awoken %d; wanted 1; now: %s", awoken, fc.Now())
	}

	// wait for manageWin to go back to sleep, and then cancel the context
	// once we've checked a few things
	fc.AwaitSleepers(1)
	if sleepers := fc.NumSleepers(); sleepers != 1 {
		t.Errorf("unexpected number of goroutines sleeping; want %d; got %d", 1, sleepers)
	}

	// now, advance 50% of the term-length so we get past the extension
	// done by the first candidate
	if awoken := fc.Advance(secondWinnerConfig.TermLength / 2); awoken != 1 {
		t.Errorf("unexpected number of sleepers awoken %d; wanted 1", awoken)
	}

	<-secondElectedCh

	if secondTV == nil {
		t.Fatalf("second candidate TimeView is nil")
	}
	{
		e, readErr := d.ReadCurrent(ctx)
		if readErr != nil {
			t.Fatalf("unexpected read error: %s", readErr)
		}
		if !e.TermExpiry.Equal(secondTV.Get()) {
			t.Errorf("unexpected timestamp in second TimeView %s; expected %s",
				e.TermExpiry, secondTV.Get())
		}
		if e.TermExpiry.Add(-secondWinnerConfig.TermLength).Before(firstTV.Get().Add(secondClockOffset)) {
			t.Errorf("second winner acquired leadership before first term ended: new begin: %s; old end: %s ",
				e.TermExpiry.Add(-secondWinnerConfig.TermLength), firstTV.Get().Add(secondClockOffset))
		}
	}

	select {
	case <-secondCtx.Done():
		t.Errorf("second candidate callback context cancelled prematurely")
	default:
	}

	cancel()

	if onElectedCalls != 1 {
		t.Errorf("unexpected number of OnElected calls: %d", onElectedCalls)
	}

}

func TestMultipleContendersWithFakeClockAndSkew(t *testing.T) {
	t.Parallel()
	wg := sync.WaitGroup{}
	defer wg.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := memory.NewDecider()

	type cState struct {
		tv        *TimeView
		ctx       context.Context
		elected   atomicBool
		c         Config
		oustingCh chan struct{}
	}
	const numContenders = 16
	const termLength = time.Minute * 20

	contenders := make([]cState, numContenders)
	baseTime := time.Now()
	fc := fake.NewClock(baseTime)
	electedCh := make(chan int, 1)

	for z := 0; z < numContenders; z++ {
		lz := z
		contenders[lz] = cState{
			oustingCh: make(chan struct{}, 1),
			c: Config{
				OnElected: func(ctx context.Context, tv *TimeView) {
					t.Logf("election win by %d", lz)
					contenders[lz].tv = tv
					contenders[lz].ctx = ctx
					contenders[lz].elected.set(true)
					electedCh <- lz
				},
				LeaderChanged: func(_ context.Context, e entry.RaceEntry) { t.Logf("%d lost election: %+v", lz, e) },
				OnOusting: func(ctx context.Context) {
					t.Logf("%d ousted", lz)
					contenders[lz].elected.set(false)
					contenders[lz].oustingCh <- struct{}{}
				},
				LeaderID:         "maybeLeader" + strconv.Itoa(lz),
				HostPort:         []string{"127.0.0.2:" + strconv.Itoa(7123+lz)},
				Decider:          d,
				TermLength:       termLength,
				ConnectionParams: []byte("bimbat" + strconv.Itoa(lz)),
				MaxClockSkew:     time.Second,
				// Give different candidates progressively earlier offsets
				Clock: offset.NewOffsetClock(fc,
					-time.Duration(lz)*time.Millisecond*8),
			},
		}
	}

	if sleepers := fc.NumSleepers(); sleepers != 0 {
		t.Errorf("unexpected number of goroutines sleeping; want %d; got %d", 0, sleepers)
	}

	for z := range contenders {
		lz := z
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := contenders[lz].c.Acquire(ctx); err != context.Canceled {
				t.Errorf("non-cancel error: %s", err)
			}

		}()
	}

	// wait until manageWin goes to sleep waiting for the next time it needs to awaken.
	fc.AwaitSleepers(numContenders)
	if sleepers := fc.NumSleepers(); sleepers != numContenders {
		t.Errorf("unexpected number of goroutines sleeping; want %d; got %d", numContenders, sleepers)
	}

	// wait for the first leader's OnElected callback to complete
	leaderID := <-electedCh
	{

		leaderTV := contenders[leaderID].tv
		if leaderTV == nil {
			t.Fatalf("first candidate's timeview is nil")
		}

		// verify that no one else thinks they won this election
	OTHERLEADERSLOOP:
		for {
			select {
			case otherLeaderID := <-electedCh:
				t.Errorf("second winner of election %d; already have %d",
					otherLeaderID, leaderID)
			default:
				break OTHERLEADERSLOOP
			}
		}

		leaders := make([]int, 0, 1)
		for z := range contenders {
			if contenders[z].elected.get() {
				leaders = append(leaders, z)
			}
		}
		if len(leaders) > 1 {
			t.Errorf("multiple leaders: %v", leaders)
		}

		expectedTermEnd := baseTime.Add(termLength - time.Duration(leaderID)*time.Millisecond*8)
		if termEnd := leaderTV.Get(); !termEnd.Equal(expectedTermEnd) {
			t.Errorf("unexpected term end in TimeView: %s; expected %s",
				termEnd, expectedTermEnd)
		}
	}

	const maxjitterTime = time.Duration(float64(termLength) * jitterTermMaxFraction)
	// Advance from our initial timestamp up to the end of the term, plus
	// 10% to get past the max jitter we add
	fc.Advance(termLength + maxjitterTime)

	// wait for manageWin to go to sleep on the leader and all the others
	// to go to sleep awaiting the next term.
	fc.AwaitSleepers(numContenders)
	if sleepers := fc.NumSleepers(); sleepers != numContenders {
		t.Errorf("unexpected number of goroutines sleeping; want %d; got %d",
			numContenders, sleepers)
	}

	{
		oldTermEnd := baseTime.Add(termLength - time.Duration(leaderID)*time.Millisecond*8)
		oldLeader := leaderID

		// we know that the looping/acquisition/manageWin goroutines
		// are all sleeping. Find out whether the old leader extended
		// its lease.
		oldleaderTV := contenders[leaderID].tv
		if oldleaderTV.Get().Equal(oldTermEnd) {
			// new leader; wait for its elected cb to complete and
			// the old one's ousting channel to get a message
			<-contenders[oldLeader].oustingCh
			leaderID = <-electedCh
		}

		leaderTV := contenders[leaderID].tv
		if leaderTV == nil {
			t.Fatalf("leader's timeview is nil")
		}

		expectedTermEnd := baseTime.Add(2*termLength + maxjitterTime - time.Duration(leaderID)*time.Millisecond*8)
		// Before we advance time, verify that we have the right
		// timestamp returned by our TimeView
		if termEnd := leaderTV.Get(); !termEnd.Equal(
			expectedTermEnd) {
			t.Errorf("unexpected term end in TimeView: %s; expected %s",
				termEnd, expectedTermEnd)
		}

		// verify that no one else thinks they won this election
	OTHERLEADERSLOOP2:
		for {
			select {
			case otherLeaderID := <-electedCh:
				t.Errorf("second winner of election %d; already have %d",
					otherLeaderID, leaderID)
			default:
				break OTHERLEADERSLOOP2
			}
		}

		cbleaders := make([]int, 0, 1)
		tvleaders := make([]int, 0, 1)
		for z := range contenders {
			if contenders[z].elected.get() {
				cbleaders = append(cbleaders, z)
			}
			if contenders[z].tv != nil && contenders[z].tv.Get().After(contenders[z].c.Clock.Now()) {
				tvleaders = append(tvleaders, z)
			}
		}
		if len(cbleaders) > 1 {
			t.Errorf("multiple leaders by callback: %v", cbleaders)
		}
		if len(tvleaders) > 1 {
			t.Errorf("multiple leaders by TimeView: %v", tvleaders)
		}

	}

	cancel()
}

func ExampleConfig_Acquire() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := memory.NewDecider()

	now := time.Date(2020, 5, 6, 0, 0, 0, 0, time.UTC)
	fc := fake.NewClock(now)

	tvLeaderIndicator := (*TimeView)(nil)
	electedCh := make(chan struct{})
	c := Config{
		Decider:  d,
		HostPort: []string{"127.0.0.1:8080"},
		LeaderID: "yabadabadoo",
		OnElected: func(ctx context.Context, tv *TimeView) {
			fmt.Printf("I won! Leader term expiration: %s; held: %t\n",
				tv.Get(), tv.ValueInFuture())
			tvLeaderIndicator = tv
			close(electedCh)
		},
		OnOusting: func(ctx context.Context) {
			// make sure we've already set `tvLeaderIndictor` before touching it
			<-electedCh
			fmt.Printf("I lost! Holding Lock: %t; expires: %s\n",
				// Note that ValueInFuture will return true
				// here because the clock is still within the
				// term.
				tvLeaderIndicator.ValueInFuture(),
				tvLeaderIndicator.Get())
		},
		LeaderChanged: func(ctx context.Context, entry entry.RaceEntry) {
			fmt.Printf("%q won\n", entry.LeaderID)
		},
		TermLength:   time.Minute * 30,
		MaxClockSkew: time.Second * 10,
		Clock:        fc,
	}
	acquireCh := make(chan error, 1)
	go func() {
		acquireCh <- c.Acquire(ctx)
	}()

	fc.AwaitSleepers(1)
	<-electedCh
	fc.Advance(c.TermLength / 2)

	// Wait for the lease renewal to happen before cancelling (so the
	// output is predictable)
	fc.AwaitSleepers(1)

	cancel()
	// Acquire blocks until all callbacks return (there's an internal WaitGroup)
	<-acquireCh
	// advance past the end of the current term (after an extension)
	fc.Advance(c.TermLength + time.Minute)
	fmt.Printf("Still Leading: %t; expiry: %s; current time: %s\n",
		tvLeaderIndicator.ValueInFuture(),
		tvLeaderIndicator.Get(), fc.Now())

	// Unordered output:
	// I won! Leader term expiration: 2020-05-06 00:30:00 +0000 UTC; held: true
	// I lost! Holding Lock: true; expires: 2020-05-06 00:45:00 +0000 UTC
	// "" won
	// Still Leading: false; expiry: 2020-05-06 00:45:00 +0000 UTC; current time: 2020-05-06 00:46:00 +0000 UTC
}
