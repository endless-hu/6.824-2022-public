# Lab 2 - Raft

[Here](http://nil.lcs.mit.edu/6.824/2021/labs/lab-raft.html) is the original lab requirement.

Implement the `Raft` peers according to [this paper](http://nil.lcs.mit.edu/6.824/2021/papers/raft-extended.pdf) to form a fault-tolerant distributed system.

## How To Design Good: A Holistic View

Before we start to design such a complex system, it's better to have a clear idea in our minds. After completing the lab, I became crystally clear about the system, but I remember that when I started to work on it, I was really hazy. Thus, it's necessary to record my ideas here and hope anyone in need could benefit from them.

### 1. High-Level Idea

 First, we have to know the high-level idea of Raft, a.k.a how it achieves consensus.

 1. There should be a *leader*. The leader receives commands from the client and replicates them to other followers. We can think of the leader as an authority. It is always correct, and all followers should repeat what the leader does.
 2. There must be someone to *be* the leader. Since we want to reach a consensus among the majority of the cluster, the best way for this is to *elect* one.
 3. When a leader knows that more than half of followers replicate its log, the leader can commit replicated logs. And when followers realize that the leader has *committed* some logs, and the followers have these logs, the followers should also *commit* the logs.
 4. When a leader crashes, the remaining machines can elect a new leader to continue the service. However, the newly elected leader should be consistent with the crashed leader. You cannot expect a peer with no log to be elected as the leader. That will delete all logs (and is disastrous!). Therefore, the election must follow some rules to prevent it from happening.

**An important rule** is that all *committed* logs should be precisely the same among all machines.

### 2. My Raft's Structure

1. Every peer has a `ticker()` thread. This thread periodically(100ms) checks whether the peer should launch a new round of elections or not. 

   If it did not receive heartbeats from the current leader for a while(400ms), it will increase its `currentTerm` and send `RequestVote` to all its peers in parallel. 

   If there's no leader merging, the `ticker()` should start the next round.

2. When a follower receives a vote request, it should check against its own log to determine whether the candidate is eligible to be a leader. 

3. If the peer receives votes from the majority, it should do `leaderInitialize()`, which sets some necessary variables and launches a new thread, `leader()`. The `leader()` thread will periodically send heartbeats to all followers in parallel and exit immediately if it finds itself no longer a leader.

4. If a follower receives an `AppenEntries()`(heartbeat) from the leader, it should check whether the newly arrived entries are consistent with its own log. 

   If the follower finds some logs absent, it should notify the leader that it fails to append these new entries. The leader should send more old logs to it, or `InstallSnapshot` if the follower falls behind too much;

   If the follower finds some logs conflict with the new entries, it should erase these logs and append the new entries.

   Finally, if the follower finds the leader committed more logs than it commits, it should follow the leader and commit these logs.

5. When the upper service calls `Snapshot(index int, snapshot []byte)`, the Raft should trim logs before `index`, and save the `snapshot` as well as the current state.

6. There should be a thread, `applier()`, running in the background monitoring those committed but unapplied logs. Once we advance the `commitIndex`, we should wake up `applier()` to apply any committed but unapplied logs.

According to the paper and the discussion above, the `Raft` data structure should be:

```go
type Raft struct {
	mu        *sync.Mutex         // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()
	applyCh   chan ApplyMsg

	//
	// persist state
	//
	voteFor     int
	currentTerm int
	commitIndex int /* index of highest log entry known to be committed
	I persist it for log catch-up optimization */
	snapshotIndex int
	log           []LogEntry

	// volatile state
	lastApplied   int // index of highest log entry applied to state machine
	lastContacted time.Time
	leaderID      int
	nextLogIndex  int

	// volatile state on leaders, reinitialized after election
	nextIndex  []int
	matchIndex []int

	// To notify rf.applier to apply committed logs
	// broadcast when commitIndex > lastApplied
	commitIndexUpdate *sync.Cond
}
```

## Design Details

### For `ticker()`

The ticker will wake up every 100ms to check if there's an election timeout. The pseudo-code is:

```go
func (rf *Raft) ticker() {
  for !rf.killed() {
    time.Sleep(100*Millisecond)
    if not leader {
      continue
    }
    if election time out {
      randomly sleep between 100~500ms
      be a candidate
      
      beLeader := make(chan bool) // The election result will be passed through this
      go rf.kickOffElectionWithTimeout(beLeader, rf.currentTerm)
      if <-beLeader {
        rf.leaderInitialize()
      }
      
      Post sleep for some time so that the whole round takes 1 second
    }
  }
}
```

*One thing to notice is that a vote request RPC may take several seconds to return, but the election timeout is only 400ms.* Thus, we cannot wait until all RPCs return. To achieve this, we can utilize the `select` feature of `Go` to set a timer for the election. If the election does not return a result in a given time, we regard the election as a *fail* and begin another round. The code looks like this:

```go
// `beLeader` returns the result of this round's election
func (rf *Raft) kickOffElectionWithTimeout(beLeader chan<- bool, term int) {
	localCh := make(chan bool)      // Reports back the election result
	go rf.kickOffElection(localCh, term int)  // This do the election work
  
	select {
	case <-time.After(500 * time.Millisecond):
		beLeader <- false
	case yes := <-localCh:
		beLeader <- yes
	}
}
```

**Another big headache** is that the term may change during an election. If that's the case, even if the candidate gathered enough votes, it should NOT enter the `leader()` thread. 

### The `kickOffElection()`

The design philosophy is to return a result as soon as possible.

**Note:** you should be careful about the `RequestVoteArgs`! For example, it's **NOT a good idea** to use `rf.currentTerm` as `args.Term`.

```go
func (rf *Raft) kickOffElection(beLeader chan<- bool, term int) {
  votes := 1  // Already received vote from myself
  refuse := 0 // RPC fail also considered as refuse
  for peer in peers {
    if peer is me {
      continue
    }
    go func(peer int) {
      peer.Call("rf.RequestVote")
      if ok {
        if reply.Term > rf.currentTerm {
          update currentTerm
          reset voteFor
          beLeader <- false
          return
        }
        if rf.currentTerm != term {
          beLeader <- false
          return
        }
        if reply.Term < rf.currentTerm {
          Drop the vote and return
        }
        if reply.Grant {
          votes++
          if votes > half of peers {
            beLeader <- true
          }
        } else {
          refuse++
          if refuse > half of peers {
            beLeader <- false
          }
        }
      } else {
        refuse++
        if refuse > half of peers {
          beLeader <- false
        }
      }
    }(peer)
  }
}
```

### The `leader()` thread

The leader thread also has to ensure that heartbeats should be sent every 100ms without being blocked by the last heartbeat. I still exploited the feature of `select` to bypass blocking:

```go
func (rf *Raft) leader(term int) {
	exitCh := make(chan struct{})
	// When the thread was first invoked, immediately send heartbeats to all peers
	go rf.heartBeat(exitCh, term)

	for !rf.killed() {
		select {
		case <-exitCh:
			reset election timer
      reset leaderID if I'm the leader
			return

		case <-time.After(110 * time.Millisecond):
			go rf.heartBeat(exitCh, term)
      update my commitIndex if new logs get replicated, and notify applier()
		}
	}
}
```

The `exitCh` reports back the result of heartbeats. If the leader cannot get successful heartbeats from the majority of the cluster, it should convert to a follower. 

### The `heartBeat()`

**The tricky issue** is that `rf.currentTerm` can be updated at any time, so in the `heartbeat`, we CANNOT use `rf.currentTerm` as the argument. Instead, we can pass in the term number when the peer was elected so that any change of term will be detected and immediately stop the heartbeat.

```go
func (rf *Raft) heartBeat(exitCh chan<- struct{}, term int) {
  rpc_fail := 0
  for peer in rf.peers {
    if peer is me {
      continue
    }
    go func(peer int) {
    	Call AppendEntries OR InstallSnapshot, based on rf.nextIndex[peer] and rf.snapshotIndex
      if ok {
        if reply.Term > rf.currentTerm {
          update my currentTerm
          exitCh <- struct{}{}
          return
        }
        if term != rf.currentTerm {
          exitCh <- struct{}{}
          return
        }
        if reply.Term < rf.currentTerm {
          Drop the reply and return
        }
        if success {
          adjust rf.nextIndex[peer] to the end of my log
                                    OR rf.snapshotIndex+1 
        } else {
          adjust rf.nextIndex[peer] to 1+commitIndex reported back by the peer
        }
      } else {
        rpc_fail++
        if rpc_fail > half of peers {
          exitCh <- struct{}{}
          return
        }
      }
    }(peer)
  }
}
```

### The `applier()`

This background thread will be waked up when `commitIndex` advances.

One thing to notice is that when sending `ApplyMsg` through `applyCh`, the `rf.mu` should NOT be held.

To avoid the problem, we can get the slice before unlock.

```go
func (rf *Raft) applier() {
  rf.mu.Lock()
  for rf.lastApplied >= rf.commitIndex {
    rf.commitIndexUpdate.Wait()
  }
  commitIndex = rf.commitIndex
  toApply := rf.log[rf.lastApplied+1:rf.commitIndex+1]
  rf.mu.Unlock()
  
  for log := range toApply {
    rf.applyCh <- ApplyMsg{...}
  }
  
  rf.lastApplied = commitIndex
}
```

### Other Normal Functions

#### For `RequestVote()`

Strictly following the paper, the pseudo-code is:

```go
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
  if args.Term < rf.currentTerm {
    refuse the request
    return
  }
  if args.Term > rf.currentTerm {
    reset voteFor, leaderID
    update currentTerm
  }
  if I have not voted in this term &&
     the candidate is at least as up-to-date as me {
    grant the vote
    reset election timer
  } else {
    refuse the vote
  }
}
```

#### For `AppendEntries()`

Strictly following the paper, we do the following:

```go
func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
  if args.Term < rf.currentTerm {
    refuse and return
  }
  if args.Term > rf.currentTerm {
    reset voteFor
    update currentTerm and leaderID
  }
  reset election timer
  if args is NOT consistent with my log {
    reply failure, and tell the leader my commitIndex
    return
  }
  Erase all logs after args.LastLogIndex
  Append all args.Entries to my log
  reply success
}
```

#### For `Snapshot()`

Note that I reserved the log at `index` because it is helpful when the leader wants to send `AppendEntries` because the `LastLogIndex` and `LastLogTerm` are often used.

```go
func (rf *Raft) Snapshot(index int, snapshot []byte) {
  rf.log = rf.log[index:]
  rf.snapshotIndex = index
  rf.persister.SaveStateAndSnapshot(encodeState(rf), snapshot)
}
```

## Design Subtleties

### Two Indexing Systems

Please refer to [myBugs.md: Two Indexing Systems](myBugs.md#my-approach-two-indexing-systems) and its context to see what it is and what problem it wants to address.

### Catch-Up Optimization

In the lecture, I heard someone proposing using `commitIndex` to speed up catch-up. However, the professor was negative about this because he thought it will cause Raft to send too many unnecessary RPCs, which leads to testing failure.

This is **NOT THE CASE!** I applied catch-up optimization using the `commitIndex` and it consumes even **less **traffic. I think backing up by one term is not as good as backing up using `commitIndex`, since a slow follower can catch up with the leader's log in **TWO ROUNDS** of heartbeats. 