package frangipani

import "time"

const (
	OK             = "OK"
	ErrNoKey       = "ErrNoKey"
	ErrWrongLeader = "ErrWrongLeader"
	ErrRejectRenew = "ErrReject"
	ErrDup         = "ErrDup"
	ErrLocked      = "ErrLocked"
)

const leaseDuration = 200 * time.Millisecond

type Err string

// Put or Append
type PutAppendArgs struct {
	KVMap map[string]string
	// You'll have to add definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
	ClerkID int64
	SeqNo   int
}

type PutAppendReply struct {
	Err Err
}

type GetArgs struct {
	Key string
	// You'll have to add definitions here.
	ClerkID int64
}

type GetReply struct {
	Err        Err
	Value      string
	IssuedTime []byte // use GobDecode to decode
}

type RenewLeaseArgs struct {
	ClerkID int64
	Keys    []string
}

type RenewLeaseReply struct {
	IssuedTime [][]byte // each element is an encoded time.Time
	Err        Err
}
