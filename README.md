![Go](https://github.com/vimeo/leaderelection/workflows/Go/badge.svg)
[![GoDoc](https://godoc.org/github.com/vimeo/leaderelection?status.svg)](https://godoc.org/github.com/vimeo/leaderelection)

# LeaderElection

LeaderElection is a library for electing a leader with a pluggable backend (a
[`RaceDecider`]).

## [`RaceDecider`]
The [`RaceDecider`] implementation needs to provide a conditional-write or
transactional-write mechanism to ensure that only one write succeeds per
election-cycle, but otherwise has very minimal requirements.

Currently, this implementation only has two implementations:
 - [Google Cloud Storage][`gcs`]: intended for easy production use while running in GCP, using a GCS object for locking.
 - [Memory][`memory`]: intended for tests.

## Electing a leader

The [`Config`] struct contains callbacks and tunables for the leader election
"campaign".

Each process that would like to acquire leadership must register callbacks for
all three of `OnElected`, `OnOusting` and `LeaderChanged`, as well as specify
unique `LeaderID` and `HostPort`s (the latter two are used for communication, so
some use-cases may be able to ignore them)

The `TermLength` is the length of time that a leader acquires leadership for,
and the length of any extension. This duration should be long enough to get
useful work done, but short enough that you won't have a problem if the leader
dies and no one takes over the remainder of the lease term. The `Config.Acquire`
method takes care of extending the lease twice per term to reduce the likelihood
of spuriously losing the lock.

`MaxClockSkew` specifies the corrections added to and subtracted from sleeps
and leases to account for a lack of perfect synchronization among the clocks of
all candidates.

`ConnectionParams` is a generic byte-payload side-channel. The `legrpc` package
uses it for the GRPC [`ServiceConfig`], but other use-cases may stash other
serialized data there (may be `nil`).

`Clock` is an instance of a `clocks.Clock`, which should be `nil` outside of
tests, in which case it uses `clocks.DefaultClock()`.

### There's more to Leadership than getting elected

The `OnElected` callback takes two arguments which indicate the state of the
leadership lock with different degrees of certainty.

The `ctx` argument is a context derived from the one passed to `Acquire` that
will be canceled upon losing the leadership role. This is an explicit
cancellation by the goroutine handling lease renewals and acquisition, and as
such is subject to normal thread-scheduling delay caveats (particularly relevant
when operating with heavy CPU-contention).

To address the thread-scheduling-delay issues plaguing use of `ctx`, the
second argument to `OnElected` is a `*TimeView` containing the current
expiration time. This pointer tracks an atomic value which is updated every time
the lease is extended.  Before taking any action that requires holding
leadership, one should always check that the time returned by `t.Get()` is in
the future.

## Picking a `RaceDecider`

The [`gcs`] [`RaceDecider`] is the only currently usable implementation. It
requires a Google Cloud Storage client, a bucket and an object.

In tests, one can use the [`memory`] [`RaceDecider`], as that implementation
is trivially fast and lacks any external dependencies, but doesn't work outside
a single process.

## Watching an Election

Leader election is useful on its own only for a subset of use cases. Many times
it is necessary for other processes to observe a leader election to send
requests to the correct process. As such, one can define a [`WatchConfig`] and
call [`Watch()`] on it. The callback will be called sequentially for every
lease-extension and acquisition, thus allowing an observer to track the
expiration of the current leader's leadership term.

[`RaceDecider`]: https://pkg.go.dev/github.com/vimeo/leaderelection?tab=doc#RaceDecider
[`WatchConfig`]: https://pkg.go.dev/github.com/vimeo/leaderelection?tab=doc#WatchConfig
[`Watch()`]: https://pkg.go.dev/github.com/vimeo/leaderelection?tab=doc#WatchConfig.Watch
[`Config`]: https://pkg.go.dev/github.com/vimeo/leaderelection?tab=doc#Config
[`ServiceConfig`]: https://github.com/grpc/grpc/blob/master/doc/service_config.md
[`gcs`]: https://pkg.go.dev/github.com/vimeo/leaderelection/gcs
[`memory`]: https://pkg.go.dev/github.com/vimeo/leaderelection/memory
