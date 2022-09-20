package frangipani

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"6.824/labgob"
	"6.824/labrpc"
	"6.824/raft"
)

const Debug = true

type Op struct {
	// Your definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
	OpType  string // either "Put" or "Get". Put will release the lock, while Get will acquire the lock.
	KVMap   map[string]string
	ClerkID int64
	SeqNo   int
}

type ClerkMetaData struct {
	appliedIndex int
	condReply    *sync.Cond // wake up when the cmd is applied or I'm not leader
}

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
	lockManager  LockManagerServer
	appliedIndex int
	clerks       map[int64]*ClerkMetaData

	logger *log.Logger
}

// ------------------------------------------------------------
//                  Services exposed to clients
// ------------------------------------------------------------

func (kv *KVServer) Get(args *GetArgs, reply *GetReply) {
	currentTerm, isleader := kv.rf.GetState()
	if !isleader {
		reply.Err = ErrWrongLeader
		return
	}
	atomic.StoreInt32(&kv.isLeader, int32(currentTerm))

	// wait for read Ready
	for {
		kv.mu.Lock()
		if kv.rf.IsReadReady(kv.appliedIndex) {
			kv.mu.Unlock()
			break
		}
		kv.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
		_, isleader = kv.rf.GetState()
		if !isleader {
			kv.logger.Printf("[GET] args: %+v. no longer leader! return...\n", args)
			reply.Err = ErrWrongLeader
			return
		}
	}

	kv.mu.Lock()
	defer kv.mu.Unlock()

	if kv.lockManager.IsLockedBy(args.Key, args.ClerkID) {
		reply.Err = ErrDup
		reply.Value = kv.kvMap[args.Key]
		var err error
		reply.IssuedTime, err = time.Now().GobEncode()
		if err != nil {
			kv.logger.Fatalf("[GET] encode time error: %v\n", err)
		}
		kv.logger.Printf("[GET] Already locked. args: %+v, reply: %+v. my state: %v\n",
			args, reply, kv.reportState())
		return
	}

	if kv.lockManager.IsLocked(args.Key) {
		kv.logger.Printf("[GET] Key %v already locked by %v. args: %+v\n",
			args.Key, kv.lockManager.LockTable[args.Key], args)
		reply.Err = ErrLocked
		return
	}

	idx, _, ok := kv.rf.Start(Op{"Get", map[string]string{args.Key: ""}, args.ClerkID, 0})
	if !ok {
		reply.Err = ErrWrongLeader
		return
	}

	for kv.appliedIndex < idx {
		kv.getClerk(args.ClerkID).condReply.Wait()
		_, isleader = kv.rf.GetState()
		if !isleader {
			reply.Err = ErrWrongLeader
			kv.logger.Printf("[GET] no longer leader! exit...\n")
			return
		}
	}
	if kv.lockManager.IsLockedBy(args.Key, args.ClerkID) {
		reply.Err = OK
		reply.Value = kv.kvMap[args.Key]
		var err error
		reply.IssuedTime, err = time.Now().GobEncode()
		if err != nil {
			kv.logger.Fatalf("[GET] encode time error: %v\n", err)
		}
	} else {
		reply.Err = ErrLocked
	}
	kv.logger.Printf("[GET] Done. args: %+v. Reply: %+v\n", args, reply)
}

func (kv *KVServer) PutAppend(args *PutAppendArgs, reply *PutAppendReply) {
	_, isleader := kv.rf.GetState()
	if !isleader {
		reply.Err = ErrWrongLeader
		return
	}
	atomic.StoreInt32(&kv.isLeader, 1)

	kv.mu.Lock()
	defer kv.mu.Unlock()

	clerk := kv.getClerk(args.ClerkID)
	if clerk.appliedIndex >= args.SeqNo {
		reply.Err = OK
		kv.logger.Printf("[PUTAPPEND] Already executed. clerk.appliedIndexPut = %v, args: %+v\n",
			clerk.appliedIndex, args)
		return
	}
	_, _, ok := kv.rf.Start(Op{"Put", args.KVMap, args.ClerkID, args.SeqNo})
	if !ok {
		reply.Err = ErrWrongLeader
		return
	}

	for clerk.appliedIndex < args.SeqNo {
		kv.logger.Printf("[PUTAPPEND] Wait. clerk.appliedIndex = %v, args: %+v\n",
			clerk.appliedIndex, args)
		kv.clerks[args.ClerkID].condReply.Wait()
		_, isleader = kv.rf.GetState()
		if !isleader {
			reply.Err = ErrWrongLeader
			kv.logger.Printf("[PUTAPPEND] no longer leader! exit...\n")
			return
		}
	}
	reply.Err = OK
	kv.logger.Printf("[PUTAPPEND] Done. args: %+v\n", args)
}

// This op does not need to be replicated and can be immediately executed on the leader.
// But leader should ensure it is the newest. i.e. ReadReady
func (kv *KVServer) RenewLease(args *RenewLeaseArgs, reply *RenewLeaseReply) {
	currentTerm, isleader := kv.rf.GetState()
	if !isleader {
		reply.Err = ErrWrongLeader
		return
	}
	atomic.StoreInt32(&kv.isLeader, int32(currentTerm))

	// wait for read Ready
	for {
		kv.mu.Lock()
		if kv.rf.IsReadReady(kv.appliedIndex) {
			kv.mu.Unlock()
			break
		}
		kv.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
		_, isleader = kv.rf.GetState()
		if !isleader {
			kv.logger.Printf("[RENEWLEASE] args: %+v. no longer leader! return...\n", args)
			reply.Err = ErrWrongLeader
			return
		}
	}

	kv.mu.Lock()
	defer kv.mu.Unlock()

	kv.logger.Printf("[RENEWLEASE] args: %+v, my state: %v\n", args, kv.reportState())

	reply.IssuedTime = make([][]byte, len(args.Keys))
	reply.Err = OK

	for i, key := range args.Keys {
		if kv.lockManager.RenewLock(key, args.ClerkID) {
			var err error
			reply.IssuedTime[i], err = time.Now().GobEncode()
			if err != nil {
				kv.logger.Fatalf("[RENEWLEASE] encode time error: %v\n", err)
			}
		}
	}
	kv.logger.Printf("[RENEWLEASE] Done. args: %+v, reply: %+v\n", args, reply)
}

// ------------------------------------------------------------
// the tester calls Kill() when a KVServer instance won't
// be needed again. for your convenience, we supply
// code to set rf.dead (without needing a lock),
// and a killed() method to test rf.dead in
// long-running loops. you can also add your own
// code to Kill(). you're not required to do anything
// about this, but it may be convenient (for example)
// to suppress debug output from a Kill()ed instance.
func (kv *KVServer) Kill() {
	atomic.StoreInt32(&kv.dead, 1)
	kv.rf.Kill()
	// Your code here, if desired.
	kv.logger.Println("------------ Killed -------------")
	kv.logger.SetOutput(io.Discard)
}

func (kv *KVServer) killed() bool {
	z := atomic.LoadInt32(&kv.dead)
	return z == 1
}

// -------------------------------------------------------------------------------
// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant key/value service.
// me is the index of the current server in servers[].
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
// the k/v server should snapshot when Raft's saved state exceeds maxraftstate bytes,
// in order to allow Raft to garbage-collect its log. if maxraftstate is -1,
// you don't need to snapshot.
// StartKVServer() must return quickly, so it should start goroutines
// for any long-running work.
func StartKVServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister, maxraftstate int) *KVServer {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(Op{})

	kv := new(KVServer)
	kv.me = me
	kv.maxraftstate = maxraftstate
	kv.persister = persister

	// You may need initialization code here.
	kv.kvMap = make(map[string]string)
	kv.lockManager.Init()
	kv.appliedIndex = 0
	kv.clerks = make(map[int64]*ClerkMetaData)

	// Initialize logger for debugging
	if Debug {
		logfile_name := fmt.Sprintf("kvserver-%v.log", me)
		// os.Remove(logfile_name)
		logfile, _ := os.OpenFile(logfile_name, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		kv.logger = log.New(logfile, "", log.Ltime|log.Lmicroseconds|log.Lshortfile)
	} else {
		kv.logger = log.New(io.Discard, "", 0)
	}

	kv.applyCh = make(chan raft.ApplyMsg)
	kv.rf = raft.Make(servers, me, persister, kv.applyCh)

	// You may need initialization code here.
	kv.installSnapshot(persister.ReadSnapshot())
	go kv.applier()
	go kv.leadershipMonitor()

	return kv
}

// -----------------------------------------------------------------
//   Background goroutine to read and apply `applyMsg` from Raft
// -----------------------------------------------------------------

func (kv *KVServer) applier() {
	for !kv.killed() {
		for msg := range kv.applyCh {
			kv.mu.Lock()
			kv.logger.Printf("Apply %+v\n", msg)
			if msg.CommandValid {
				if msg.CommandIndex <= kv.appliedIndex {
					kv.logger.Printf("[APPLIER] Current Index: %v, msg.Index: %v. Skip!\n",
						kv.appliedIndex, msg.CommandIndex)
					kv.mu.Unlock()
					continue
				}
				kv.applyCmd(msg)
			} else if msg.SnapshotValid {
				kv.installSnapshot(msg.Snapshot)
			}
			kv.mu.Unlock()
		}
	}
}

// Called with kv.mu locked
func (kv *KVServer) applyCmd(msg raft.ApplyMsg) {
	op := msg.Command.(Op)
	defer kv.wakeUpClerk(op.ClerkID)

	clerk := kv.getClerk(op.ClerkID)
	// Skip the message if it was executed before
	if op.OpType == "Put" && op.SeqNo <= clerk.appliedIndex {
		kv.logger.Printf("Skip the message: %+v. Current applied Put seq no: %v\n",
			op, clerk.appliedIndex)
		if kv.appliedIndex < msg.CommandIndex {
			kv.appliedIndex = msg.CommandIndex
		}
		return
	}

	if op.OpType == "Put" {
		for key, val := range op.KVMap {
			kv.kvMap[key] = val
			kv.lockManager.Unlock(key)
		}
		clerk.appliedIndex = op.SeqNo
	} else if op.OpType == "Get" {
		for key := range op.KVMap {
			kv.lockManager.Lock(key, op.ClerkID)
		}
	}

	kv.logger.Printf("Applied Op: %+v. ClerkInfo: {appliedIndex: %v}\n",
		op, kv.clerks[op.ClerkID].appliedIndex)

	kv.appliedIndex = msg.CommandIndex
}

// -----------------------------------------------------------------
//            Utility functions for the KVServer
// -----------------------------------------------------------------

// Must be called with kv.mu locked
func (kv *KVServer) getClerk(clerkID int64) *ClerkMetaData {
	if _, ok := kv.clerks[clerkID]; !ok {
		kv.clerks[clerkID] = &ClerkMetaData{
			appliedIndex: 0,
			condReply:    sync.NewCond(&kv.mu),
		}
	}
	return kv.clerks[clerkID]
}

func (kv *KVServer) wakeUpClerk(clerkID int64) {
	kv.getClerk(clerkID).condReply.Signal()
}

// Broadcast all conds to stop waiting because I'm not a leader
func (kv *KVServer) broadcastNoLeader() {
	for _, clerk := range kv.clerks {
		clerk.condReply.Broadcast()
	}
}

// -----------------------------------------------------------------
//                      Snapshot Functions
// -----------------------------------------------------------------

// Only take snapshot when Raft's log is big enough
func (kv *KVServer) takeSnapshot() {
	if kv.maxraftstate == -1 {
		return
	}
	kv.mu.Lock()
	defer kv.mu.Unlock()
	if kv.persister.RaftStateSize() >= kv.maxraftstate {
		kv.logger.Printf("Taking snapshot... Current state: %s\n", kv.reportState())
		kv.rf.Snapshot(kv.appliedIndex, kv.encodeState())
	}
}

// Must hold kv.mu when calling this function
func (kv *KVServer) installSnapshot(data []byte) {
	if data == nil || len(data) < 1 {
		return
	}
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	var appliedIndex int
	var kvMap map[string]string
	var lockManager LockManagerServer
	var clerksAppliedIndex map[int64]int
	if d.Decode(&appliedIndex) != nil ||
		d.Decode(&kvMap) != nil ||
		d.Decode(&lockManager) != nil ||
		d.Decode(&clerksAppliedIndex) != nil {
		log.Fatalln("Failed to read persistent state")
	}

	kv.logger.Printf("Snapshot to install: {appliedIndex: %v, \nkvMap: %v, \nlock manager: %+v,\nclerksAppliedIndex: %+v}\n",
		appliedIndex, kvMap, lockManager, clerksAppliedIndex)
	if appliedIndex <= kv.appliedIndex {
		kv.logger.Println("Snapshot is older than current state. My state:", kv.reportState())
		return
	}
	kv.logger.Printf("Installing snapshot. Current state: %s\n", kv.reportState())

	kv.appliedIndex = appliedIndex
	kv.kvMap = kvMap
	kv.lockManager = lockManager
	for clerkID, appliedIndex := range clerksAppliedIndex {
		clerk := kv.getClerk(clerkID)
		clerk.appliedIndex = appliedIndex
	}
}

// Must hold kv.mu when calling this function
func (kv *KVServer) encodeState() []byte {
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(kv.appliedIndex)
	e.Encode(kv.kvMap)
	e.Encode(kv.lockManager)
	// encode every clerk's applided index
	clerksInfo := make(map[int64]int)
	for clerkID, clerk := range kv.clerks {
		clerksInfo[clerkID] = clerk.appliedIndex
	}
	e.Encode(clerksInfo)
	return w.Bytes()
}

func (kv *KVServer) reportState() string {
	return fmt.Sprintf(`
		KVServer: {
			appliedIndex: %v,
			kvMap: %v,
			clerks: %s,
			lock manager: %+v,
		}`, kv.appliedIndex, kv.kvMap, kv.reportClerks(), kv.lockManager)
}

func (kv *KVServer) reportClerks() string {
	var clerksInfo string = "["
	for clerkID, clerk := range kv.clerks {
		clerksInfo += fmt.Sprintf("{clerkID: %v, appliedIndex: %v}, ",
			clerkID, clerk.appliedIndex)
	}
	clerksInfo += "]"
	return clerksInfo
}

// ------------------------ End Utility -------------------------------

// Background go routine to monitor leader state changes
func (kv *KVServer) leadershipMonitor() {
	for !kv.killed() {
		time.Sleep(100 * time.Millisecond)
		kv.takeSnapshot()
		// Don't check if I'm not the leader
		if atomic.LoadInt32(&kv.isLeader) == 0 {
			continue
		}
		_, isleader := kv.rf.GetState()
		if !isleader {
			// no longer leader, update status
			kv.mu.Lock()
			atomic.StoreInt32(&kv.isLeader, 0)
			kv.broadcastNoLeader()
			kv.logger.Printf("No longer leader! Exit all RPCs\n")
			kv.mu.Unlock()
		}
	}
}
