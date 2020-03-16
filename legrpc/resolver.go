package legrpc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc/resolver"

	"github.com/vimeo/leaderelection"
	"github.com/vimeo/leaderelection/clocks"
	"github.com/vimeo/leaderelection/entry"
)

// Resolver implements google.golang.org/grpc/resolver.Resolver
type Resolver struct {
	cc        resolver.ClientConn
	cancel    context.CancelFunc
	errch     <-chan error
	events    chan struct{}
	reresolve chan<- struct{}
	wg        sync.WaitGroup
}

// ResolveNow will be called by gRPC to try to resolve the target name
// again. It's just a hint, resolver can ignore this if it's not necessary.
//
// It could be called multiple times concurrently.
func (r *Resolver) ResolveNow(_ resolver.ResolveNowOptions) {
	// do a non-blocking write to the reresolve channel
	select {
	case r.reresolve <- struct{}{}:
	default:
	}
}

func (r *Resolver) handleEntry(rentry *entry.RaceEntry) {
	// poke the events channel in case someone's paying attention
	defer func() {
		select {
		case r.events <- struct{}{}:
		default:
		}
	}()
	state := resolver.State{}

	if time.Since(rentry.TermExpiry) > 0 {
		// the current term expired
		r.cc.UpdateState(state)
		return
	}

	// If the current term hasn't expired yet, set the
	// hostport appropriately
	state.Addresses = []resolver.Address{resolver.Address{
		Addr: rentry.HostPort,
		// this field intentionally left blank (per advice in
		// the library's docstring)
		ServerName: "",
		Attributes: nil,
	}}
	if len(rentry.ConnectionParams) > 0 {
		state.ServiceConfig = r.cc.ParseServiceConfig(string(rentry.ConnectionParams))
	}
	r.cc.UpdateState(state)

}

// Close closes the resolver.
func (r *Resolver) Close() {
	// cancel the watching goroutine's context and await the exit
	r.cancel()
	defer r.wg.Wait()
	<-r.errch
}

// ResolverBuilder implements google.golang.org/grpc/resolver.Builder
type ResolverBuilder struct {
	decider leaderelection.RaceDecider
	clock   clocks.Clock
}

// NewResolverBuilder creates a new ResolverBuilder wrapping the passed
// RaceDecider
func NewResolverBuilder(d leaderelection.RaceDecider, clock clocks.Clock) *ResolverBuilder {
	return &ResolverBuilder{
		decider: d,
		clock:   clock,
	}
}

// Build creates a new resolver for the given target.
//
// gRPC dial calls Build synchronously, and fails if the returned error is
// not nil.
// This implementation ignores the target and simply wraps the encapsulated
// `decider` in a Resolver.
func (r *ResolverBuilder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	ctx, cancel := context.WithCancel(context.Background())
	errch := make(chan error, 1)
	events := make(chan struct{}, 1)
	reresolveCh := make(chan struct{}, 1)
	res := Resolver{
		cc:        cc,
		cancel:    cancel,
		errch:     errch,
		events:    events,
		reresolve: reresolveCh,
	}
	watchCfg := leaderelection.WatchConfig{
		Decider: r.decider,
		Clock:   r.clock,
	}
	res.wg.Add(2)
	go func() {
		defer res.wg.Done()
		watchErr := watchCfg.Watch(ctx, func(ctx context.Context, rentry entry.RaceEntry) {
			res.handleEntry(&rentry)
		})
		errch <- watchErr
		if watchErr != nil && watchErr != context.Canceled {
			cc.ReportError(watchErr)
		}
	}()
	go func() {
		defer res.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-reresolveCh:
				rentry, readerr := r.decider.ReadCurrent(ctx)
				if readerr != nil {
					cc.ReportError(fmt.Errorf("failed to read current value: %w", readerr))
					continue
				}
				res.handleEntry(rentry)
			}
		}
	}()

	// await the first event (or a failure):
	select {
	case <-events:
	case watchErr := <-errch:
		cancel()
		return nil, fmt.Errorf("failed to initialize watcher: %w", watchErr)
	}

	return &res, nil

}

// Scheme returns the scheme supported by this resolver.
// Scheme is defined at https://github.com/grpc/grpc/blob/master/doc/naming.md.
func (r *ResolverBuilder) Scheme() string {
	return "leaderelection"
}
