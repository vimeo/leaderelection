package entry

import "time"

// ElectionNumber is a monotonically incrementing counter, starting with 0.
// the value -1 is special and signifies No elections (see NoElections)
type ElectionNumber int64

// NoElections indicates that no Elections have taken place.
const NoElections ElectionNumber = -1

// LeaderToken contains implementation-specific metadata about the existing
// values, returned by `WriteEntry`.
type LeaderToken interface {
	CommitTS() time.Time
}

// RaceEntry describes the contents of the file/row stored by the RaceDecider
type RaceEntry struct {
	// Unique ID of the leader (possible or current)
	LeaderID string
	// HostPort is a slice of the system's unicast interface addresses to which clients should connect.
	HostPort []string
	// Expiration-time of the term this is an election for
	TermExpiry time.Time
	// Monotonically increasing Election/Generation-number
	ElectionNumber ElectionNumber

	// ConnectionParams should be used as a side-channel for
	// leader-election metadata for the legrpc package, e.g. we use it for
	// storing the GRPC ServiceConfig (or nothing).
	ConnectionParams []byte

	// Implementation-specific metadata about the existing values that
	// should be passed back when calling `WriteEntry`.
	Token LeaderToken
}
