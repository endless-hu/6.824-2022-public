# Lab 4: ShardKV

[Here](http://nil.lcs.mit.edu/6.824/2022/labs/lab-shard.html) is the original lab requirement.

## Goal

Build a key-value storage system that *partitions* the key over a set of replica groups. 

A *shard* is a subset of the total key-value pairs, and each replica group handles multiple shards. The distribution of shards should be balanced so that the system's throughput can be maximized. 

## Lab 4A: Shard Controller

### Basic Idea

We can directly modify lab3's code to implement the shard controller. They share the same structure. The only difference is that the shard controller should maintain a slice of configs instead of a key-value map.

**NOTE**: The `slice` and `map ` in Go are *references*. Simple assignment `=` will make the variable points to the same map or slice. Therefore, when appending a modified config to the config slice, we have to do a **deep copy**, for example:

```go
func (cfg *Config) Copy() Config {
	newConfig := Config{}
	newConfig.Num = cfg.Num
	newConfig.Shards = cfg.Shards
	newConfig.Groups = make(map[int][]string)
	for gid, servers := range cfg.Groups {
		newConfig.Groups[gid] = servers
	}
	return newConfig
}
```

### The Server Struct

```go
type ClerkMetaData struct {
	appliedIndex int
	condReply    *sync.Cond // wake up when the cmd is applied or I'm not leader
}

type ShardCtrler struct {
	mu      sync.Mutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg

	// Your data here.
	isLeader  int32
	kill      int32

	clerks       map[int64]*ClerkMetaData
	appliedIndex int

	configs []Config // indexed by config num

	logger *log.Logger
}
```

### Core Algorithm: Rebalancing

We must **ensure the determinism** of the rebalancing algorithm. 

#### A Flawed Idea

It is a natural idea to:

1. Compute how many shards each group should handle after rebalancing.
2. Count how many shards each group currently handles.
3.  Take shards from groups with surplus shards, and assign them to groups with fewer shards until shards are balanced.

#### The Indeterministic Factors

1. When counting shards each group has, we usually use a **map** whose **iteration order is random**.
2. We must start taking shards from the group with **the most** shards. However, there could be multiple such groups, and sorting leaves **a random order**.

#### My Algorithm

**Always start iterating in group ID order**. During the iteration: 

If a group has fewer shards than expected, then we start another round of iteration from beginning to end, taking any shards from groups with the most shards (big number first);

If the group still owns fewer shards, we start a round of iteration again, taking shards from groups with shards more than expected. 

The time complexity is $O(N^2)$, but it satisfies both **determinism** and **minimum transfer**.

## Lab 4B: Shard KV Server

### The Storage Data Structure

Because we need to transfer shards among groups, the idea of putting all key-value pairs on a giant `map` is no longer convenient. It is better to make every shard self-maintained:

```go
type Shard struct {
	ShardID int
	// map ClientID -> SeqNo. Now we keep SeqNo **per shard per client**.
	ClerkAppliedIndex map[int64]int64
	KvMap             map[string]string
}
```

And we provide APIs for putting and getting key-value pairs from a shard. *Note* that when doing write operations, we have to provide the `clerkID` and `seqNo` so that the shard can eliminate duplicated operations:

```
func (sd *Shard) Get(key string) (string, bool) {
	if sd.KvMap == nil {
		sd.KvMap = make(map[string]string)
	}
	if v, ok := sd.KvMap[key]; ok {
		return v, true
	}
	return "", false
}

func (sd *Shard) Put(key string, value string, clerkID int64, seqNo int64) bool {
	if sd.ClerkAppliedIndex == nil {
		sd.ClerkAppliedIndex = make(map[int64]int64)
	}
	if seqNo != sd.ClerkAppliedIndex[clerkID]+1 {
		return false
	}
	sd.ClerkAppliedIndex[clerkID] = seqNo
	if sd.KvMap == nil {
		sd.KvMap = make(map[string]string)
	}
	sd.KvMap[key] = value
	return true
}

func (sd *Shard) Append(key string, value string, clerkID int64, seqNo int64) bool {
	if sd.ClerkAppliedIndex == nil {
		sd.ClerkAppliedIndex = make(map[int64]int64)
	}
	if seqNo != sd.ClerkAppliedIndex[clerkID]+1 {
		return false
	}
	sd.ClerkAppliedIndex[clerkID] = seqNo
	if sd.KvMap == nil {
		sd.KvMap = make(map[string]string)
	}
	sd.KvMap[key] += value
	return true
}

func (sd *Shard) GetClerkAppliedIndex(clerkID int64) int64 {
	if sd.ClerkAppliedIndex == nil {
		return 0
	}
	return sd.ClerkAppliedIndex[clerkID]
}
```

Now we can conveniently transfer these shards among replica groups.

Moreover, to support the management of these shards, it is better to wrap `Shard` into `InMemoryShard`:

```go
type InMemoryShard struct {
	ConfigVersion int
	Cleaned       bool
	Shard         Shard
}
```

When the shard's `ConfigVersion` is smaller than the current config, the server should deny any operations on this shard except cleaning the shard.

### The Server Struct

```go
type ShardKV struct {
	mu           sync.Mutex
	me           int
	rf           *raft.Raft
	applyCh      chan raft.ApplyMsg
	dead         int32
	persister    *raft.Persister
	make_end     func(string) *labrpc.ClientEnd
	gid          int
	ctrlers      []*labrpc.ClientEnd
	mck          *shardctrler.Clerk
	maxraftstate int // snapshot if log grows this big

	// Your definitions here.
	config       shardctrler.Config // PERSIST in snapshot
	nextConfig   shardctrler.Config // Persist in snapshot. If config is update-to-date, nextConfig is the same as config
	newestConfig shardctrler.Config

	shardsToRetrieve map[int][]int // gid -> shardIDs
	cond             *sync.Cond    // used to notify the ingestConfigChange routine that the config has been ingested

	isLeader int32 // not leader->0, leader->currentTerm

	kvMap        []InMemoryShard // PERSIST in snapshot
	appliedIndex int             // PERSIST in snapshot
	clerkConds   map[int64]*sync.Cond

	logger *log.Logger
}
```

### How To Hand Over Shards

There are two ways to hand over shards:

1. **The receiver calls the holder to retrieve the necessary shards.**
2. The holder calls the receiver to receive shards.

I chose the first solution because it can reduce implementation overhead. Specifically, suppose I adopted the second mechanism.  In that case, during reconfiguration, a group has to call other groups to receive its shards and wait for other groups to hand over shards to itself; on the contrary, I only need to retrieve shards from other groups, if I implement the first solution. 

Here are the RPC parameters when retrieving shards:

```go
type RetrieveShardsArgs struct {
  GID      int // requester's GID (maybe redundant)
	ConfigID int // Only when `ConfigID > shard.ConfigVersion` can a leader give the shard 
	ShardIDs []int // shards to be retrieved
}

type RetrieveShardsReply struct {
	ShardsData []byte // encoded []Shard
	Err        Err
}
```

### How To Reconfigure

#### 1. Detect New Config

- The leader will poll the shard controller every 100ms to see if there is any new config. 
- If it detects one, it will `Start` the command `UpdateConfig` with the new config to the Raft.
- Later it will get the `UpdateConfig` command from `applyCh`. Assign the new config to `kv.nextConfig`. 
- Compute the shard the group should retrieve, store the result into `kv.shardsToRetrieve`.

#### 2. Retrieve Shards

- The leader realizes that `kv.shardsToRetrieve` is not empty, so it should retrieve shards from other groups. 
- After it retrieves shards, it `Start`s a command `ReceiveShards` with the `ShardsData`.
- Later it will get the `ReceiveShards` command from `applyCh`, decode the `ShardsData` and accommodate these shards.

#### 3. Complete the Reconfiguration

- When accommodating shards, the server will delete the corresponding record in `kv.shardsToRetrieve`. 
- When `kv.shardsToRetrieve` is empty again, the server completes reconfiguration and update the config to `kv.nextConfig`.

