# Lab 3 - KV Raft

[Here](http://nil.lcs.mit.edu/6.824/2022/labs/lab-kvraft.html) is the original lab requirement.

Build a fault-tolerant key-value storage service upon Raft(lab2).

## Basic Idea

### Client Side

#### The struct of a client

```go
type Clerk struct {
	servers  []*labrpc.ClientEnd
	leaderID int64 // What I think the leader is
	myID     int64
	cmdSeqNo int // first command is number 1

	successiveWrite int  // Used to detect if it's under SpeedTest
	opBuf           []Op // When under speed test, we use this buffer to accelerate PutAppend

	logger *log.Logger
}
```

#### Important Features of Client

1. Clients should maintain an `SeqNo` to uniquely distinguish each command, so that when it resends commands due to network failure, no commands will be executed by server twice. However, read operations do NOT modify server state, so `Get` does NOT need an `SeqNo`.
2. It is a good idea to cache the `leaderID` to save the time try all servers in a Raft cluster.

Pseudo-code for `Get`:

```go
func (ck *Clerk) Get(key string) string {
  // Get until leader returns a result
  for {
    if leaderID cached {
      Call leader
      if reply.OK {
        return reply.Value
      } 
      // Else, leader unreachable OR no longer leader
      Clear leaderID cache
    }
    for each server {
      Call server
      if reply.OK {
        cache leaderID = server
        return reply.Value
      }
    }
  }
}
```

Pseudo-code for `PutAppend`:

```go
func (ck *Clerk) PutAppend(key string) {
  increase SeqNo
  attach SeqNo into calling args
  // Try until leader replies with OK
  for {
    if leaderID cached {
      Call leader
      if reply.OK {
        return
      } 
      // Else, leader unreachable OR no longer leader
      Clear leaderID cache
    }
    for each server {
      Call server
      if reply.OK {
        cache leaderID = server
        return
      }
    }
  }
}
```

### Server Side

#### Server Struct

```go
type KVServer struct {
	mu        sync.Mutex
	me        int
	rf        *raft.Raft
	applyCh   chan raft.ApplyMsg
	dead      int32 // set by Kill()
	persister *raft.Persister
	isLeader  int32 // not leader->0, leader->currentTerm

	maxraftstate int // snapshot if log grows this big

	// Your definitions here.
	kvMap        map[string]string
	appliedIndex int
	clerks       map[int64]*ClerkMetaData

	logger *log.Logger
}

type ClerkMetaData struct {
	appliedIndex int
	condReply    *sync.Cond // wake up when the cmd is applied or I'm not leader
}
```

#### For RPC stubs exposed to clients

```go
func (kv *KVServer) Get(args *GetArgs, reply *GetReply) {
  if I am NOT leader {
    reply.Err = ErrWrongLeader
    return
  }
  for kv.rf.IsReadReady() {
    time.Sleep(100*ms)
    if I am not leader now {
      reply.Err = ErrWrongLeader
      return
    }
  }
  reply.Value, reply.Ok = kv.kvMap[args.Key]
}
```

```go
func (kv *KVServer) PutAppend(args *PutAppendArgs, reply *PutAppendReply) {
  if I am NOT leader {
    reply.Err = ErrWrongLeader
    return
  }
  kv.raft.Start(Op{command args...})

  kv.mu.Lock()
  defer kv.mu.Unlock()
  for command not executed {
    kv.clerks[args.ClerkID].condReply.Wait()
    if I am not leader now {
      reply ErrWrongLeader
      return
    }
  }
  reply OK
}
```

#### Background Go Routines

Background Goroutines play important roles in notifying pending RPCs to return. You can use either *conditional variable* or *channel*.  No matter what measures you adopt, make sure that you know all situations in which the RPCs should return:

1. When the command is applied;
2. When the server is no longer the leader.

You will probably need another go routine to periodically check if it's still the leader. Otherwise, if the server loses its leadership after it starts a command, the RPC may be blocked forever if there are no other clients submitting commands to the server.

One routine should monitor the `ApplyCh`:

```go
func (kv *KVServer) applier() {
  for msg := range kv.applyCh {
    if msg.CommandValid {
      apply msg.Cmd
    } else if msg.SnapshotValid {
      apply msg.Snapshot
    }
    wake up RPCs waiting on this command

    if raft state too large {
      kv.takeSnapshot()
    }
  }
}
```

One routine should monitor the leadership status:

```go
func (kv *KVServer) leadershipMonitor {
  for !kv.killed() {
    if I am not leader {
      broadcast all pending RPCs
    }
  }
  time.Sleep(100*ms)
}
```

## One Remaining Issue: Performance

### Current Problem: One Command Per Heartbeat

Because the client will not execute the next command until the previous one is applied, **each heartbeat** can only carry **one command** from **one client**, which cannot satisfy the `SpeedTest`.

### My Temporary Solution

To deal with it, I use a trick in the client to detect whether it is under `SpeedTest`. If it is, it will cache the `PutAppend` instructions and return immediately so the tester can send the next command. When the cache is large enough(up to 20 commands), it will call a batch `PutAppend` on the leader so that the leader can carry 20 commands in one round heartbeat.

**However,** this solution **CAN NOT** guarantee **linearizability**!!! If a `PutAppend` operation returns, all subsequent `Get` should see its effect. However, in this cache optimization, the `PutAppend` operation may still be in the client's cache when it returns. 

### Ultimate Solution: Cache Consistency

A better idea to achieve BOTH speed and linearizability is to implement cache coherence, according to [the idea from Frangipani](http://nil.lcs.mit.edu/6.824/2022/papers/thekkath-frangipani.pdf).

In that case, an additional fault-tolerant `LockServer` is required to manage locks:

- When a client wants to **read**, it gets *S-lock* over the key and caches the value with *a lease*;
- When a client wants to **write**, it gets X-lock over the key, caches the value with a lease, continuously renews the lease, and performs all writes locally. 
- When a client **releases the lock**, it should ensure all writes were applied to the server. 
- When another client wants to **get a lock over the same key**, the Lock Server can either:
  
  1. Wait for the previous client to release the lock;
  2. Wait for the previous lease to expire;
  3. Issue a `Revoke` RPC to the client to revoke the previous lease and let the client flush all writes.