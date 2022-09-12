package frangipani

import (
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"math/big"
	"os"
	"strconv"
	"sync"
	"time"

	"6.824/labrpc"
)

type Clerk struct {
	servers  []*labrpc.ClientEnd
	leaderID int64 // What I think the leader is
	myID     int64

	mu          sync.Mutex
	cond        *sync.Cond
	getCmdSeqNo int // first command is number 1
	putCmdSeqNo int
	kvMap       map[string]string
	lockManager LockManagerClient

	logger *log.Logger
}

func nrand() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := rand.Int(rand.Reader, max)
	x := bigx.Int64()
	return x
}

func MakeClerk(servers []*labrpc.ClientEnd) *Clerk {
	ck := new(Clerk)
	ck.servers = servers
	ck.leaderID = -1
	ck.myID = nrand()
	ck.cond = sync.NewCond(&ck.mu)
	ck.lockManager.Init()
	ck.kvMap = make(map[string]string)

	// Initialize logger for debugging
	if Debug {
		logfile_name := fmt.Sprintf("clerk-%v.log", ck.myID)
		// os.Remove(logfile_name)
		logfile, _ := os.OpenFile(logfile_name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0777)
		ck.logger = log.New(logfile, "", log.Ltime|log.Lmicroseconds|log.Lshortfile)
	} else {
		ck.logger = log.New(io.Discard, "", 0)
	}
	go ck.renewer()
	return ck
}

// fetch the current value for a key.
// returns "" if the key does not exist.
// keeps trying forever in the face of all other errors.
//
// you can send an RPC with code like this:
// ok := ck.servers[i].Call("KVServer.Get", &args, &reply)
//
// the types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. and reply must be passed as a pointer.
func (ck *Clerk) Get(key string) string {
	ck.mu.Lock()
	defer ck.mu.Unlock()

	for ck.lockManager.LockExpired(key) {
		if !ck.lockManager.IsLocked(key) {
			ck.get(key)
		} else {
			ck.logger.Printf("[GET] {%v} key %v expired, waiting for renewer to renew it...\n",
				strconv.FormatInt(ck.myID, 10)[:4], key)
			ck.cond.Wait()
		}
	}

	return ck.kvMap[key]
}

// shared by Put and Append.
//
// you can send an RPC with code like this:
// ok := ck.servers[i].Call("KVServer.PutAppend", &args, &reply)
//
// the types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. and reply must be passed as a pointer.
func (ck *Clerk) PutAppend(key string, value string, op string) {
	// Comment the following line to unleash the power of Frangipani
	time.Sleep(5 * time.Millisecond)

	ck.mu.Lock()
	defer ck.mu.Unlock()

	for ck.lockManager.LockExpired(key) {
		if !ck.lockManager.IsLocked(key) {
			ck.get(key)
		} else {
			ck.logger.Printf("[PUT] {%v} key %v expired, waiting for renewer to renew it...\n",
				strconv.FormatInt(ck.myID, 10)[:4], key)
			ck.cond.Wait()
		}
	}

	if op == "Put" {
		ck.kvMap[key] = value
	} else {
		ck.kvMap[key] += value
	}
}

func (ck *Clerk) Put(key string, value string) {
	ck.PutAppend(key, value, "Put")
}
func (ck *Clerk) Append(key string, value string) {
	ck.PutAppend(key, value, "Append")
}

// renewer periodically renews the lock of the key.
// if the leader rejects to renew some locks, I will call `put`
// to flush the value to the server and release the locks.
func (ck *Clerk) renewer() {
	for {
		time.Sleep(100 * time.Millisecond)
		ck.mu.Lock()

		// renew all locks in the lock manager
		args := RenewLeaseArgs{ClerkID: ck.myID, Keys: ck.lockManager.GetLockedKeySet()}
		if len(args.Keys) == 0 {
			// no need to renew
			ck.mu.Unlock()
			continue
		}

		// Call until the leader returns
		for {
			// call the cached leader first
			if ck.leaderID != -1 {
				reply := RenewLeaseReply{}
				ok := ck.servers[ck.leaderID].Call("KVServer.RenewLease", &args, &reply)
				if ok && reply.Err == OK {
					ck.processRenew(args, reply)
					break
				}
				ck.leaderID = -1
			}
			// randomly try other servers
			tryServer := nrand() % int64(len(ck.servers))
			reply := RenewLeaseReply{}
			ok := ck.servers[tryServer].Call("KVServer.RenewLease", &args, &reply)
			if ok && reply.Err == OK {
				ck.leaderID = tryServer
				ck.processRenew(args, reply)
				break
			}
		}

		ck.mu.Unlock()
	}
}

// Update the lock manager with the new lock information
// Called with the ck.mu held
func (ck *Clerk) processRenew(args RenewLeaseArgs, reply RenewLeaseReply) {
	defer ck.cond.Broadcast()
	kvsToFlush := make(map[string]string)
	for i, issuedTimeData := range reply.IssuedTime {
		var issuedTime time.Time
		if len(issuedTimeData) > 1 {
			if issuedTime.GobDecode(issuedTimeData) != nil {
				log.Fatalf("[processRenew] Error decoding issuedTime. reply: %+v\n", reply)
			}
		}
		ck.lockManager.RenewLock(args.Keys[i], issuedTime)
		if issuedTime.IsZero() {
			kvsToFlush[args.Keys[i]] = ck.kvMap[args.Keys[i]]
		}
	}
	if len(kvsToFlush) > 0 {
		ck.put(kvsToFlush)
	}
}

// Get the lock from the remote server, store the value into ck.kvMap
// Called with ck.mu locked.
func (ck *Clerk) get(key string) {
	ck.logger.Printf("[get] {%v} Try to get the lock of the key %v\n", strconv.FormatInt(ck.myID, 10)[:4], key)

	ck.getCmdSeqNo++
	args := GetArgs{key, ck.myID, ck.getCmdSeqNo}

	for {
		if ck.leaderID != -1 {
			reply := GetReply{}
			if ck.servers[ck.leaderID].Call("KVServer.Get", &args, &reply) {
				ck.logger.Printf("[get] {%v} Try to get the lock of the key %v from the leader %v, reply: %+v\n",
					strconv.FormatInt(ck.myID, 10)[:4], key, ck.leaderID, reply)
				if reply.Err == OK {
					var issuedTime time.Time
					if issuedTime.GobDecode(reply.IssuedTime) != nil {
						log.Fatalf("[get] Error decoding issuedTime")
					}
					ck.logger.Printf("[get] {%v} key %v locked, issued time: %v\n\n",
						strconv.FormatInt(ck.myID, 10)[:4], key, issuedTime)
					ck.lockManager.Lock(key, issuedTime)
					ck.kvMap[key] = reply.Value
					return
				} else if reply.Err == ErrLocked {
					ck.mu.Unlock()
					ck.logger.Printf("[get] {%v} the key %v is locked, wait for the lock to be released\n",
						strconv.FormatInt(ck.myID, 10)[:4], key)
					time.Sleep(200 * time.Millisecond)
					ck.mu.Lock()
					ck.getCmdSeqNo++
					args.SeqNo = ck.getCmdSeqNo
					ck.logger.Printf("[get] {%v} retry to get the key %v. args: %+v\n",
						strconv.FormatInt(ck.myID, 10)[:4], key, args)
					continue
				} else if reply.Err == ErrDup {
					var issuedTime time.Time
					if issuedTime.GobDecode(reply.IssuedTime) != nil {
						log.Fatalf("[GET] Error decoding issuedTime")
					}
					ck.logger.Printf("[get] {%v} key %v: leader returns %v, issued time: %v\n\n",
						strconv.FormatInt(ck.myID, 10)[:4], key, reply.Err, issuedTime)
					ck.lockManager.Lock(key, issuedTime)
					ck.kvMap[key] = reply.Value
					return
				}
			}
			ck.leaderID = -1
		}

		tryServer := nrand() % int64(len(ck.servers))
		reply := GetReply{}
		if ck.servers[tryServer].Call("KVServer.Get", &args, &reply) {
			if reply.Err != ErrWrongLeader {
				ck.logger.Printf("[get] {%v} Try to get the lock of the key %v from the server %v, reply: %+v\n",
					strconv.FormatInt(ck.myID, 10)[:4], key, ck.leaderID, reply)
			}
			if reply.Err == OK {
				ck.leaderID = tryServer
				var issuedTime time.Time
				if issuedTime.GobDecode(reply.IssuedTime) != nil {
					log.Fatalf("[get] Error decoding issuedTime")
				}
				ck.logger.Printf("[get] {%v} key %v: leader %v returns %+v, issuedTime: %v\n\n",
					strconv.FormatInt(ck.myID, 10)[:4], key, tryServer, reply.Err, issuedTime)
				ck.lockManager.Lock(key, issuedTime)
				ck.kvMap[key] = reply.Value
				return
			} else if reply.Err == ErrLocked {
				ck.leaderID = tryServer
				ck.logger.Printf("[get] {%v} the key %v is locked, wait for the lock to be released\n",
					strconv.FormatInt(ck.myID, 10)[:4], key)
				ck.mu.Unlock()
				time.Sleep(200 * time.Millisecond)
				ck.mu.Lock()
				ck.getCmdSeqNo++
				args.SeqNo = ck.getCmdSeqNo
				ck.logger.Printf("[get] {%v} retry to get the key %v. args: %+v\n",
					strconv.FormatInt(ck.myID, 10)[:4], key, args)
				continue
			} else if reply.Err == ErrDup {
				var issuedTime time.Time
				if issuedTime.GobDecode(reply.IssuedTime) != nil {
					log.Fatalf("[GET] Error decoding issuedTime")
				}
				ck.logger.Printf("[get] {%v} key %v: leader %v returns %v, issued time: %v\n\n",
					strconv.FormatInt(ck.myID, 10)[:4], key, tryServer, reply.Err, issuedTime)
				ck.lockManager.Lock(key, issuedTime)
				ck.kvMap[key] = reply.Value
				return
			}
		}
	}
}

// Flush kv pairs which are no longer locked by me.
func (ck *Clerk) put(kvMap map[string]string) {
	ck.logger.Printf("[put] {%v} Try to flush keys: %v\n", strconv.FormatInt(ck.myID, 10)[:4], extractKeys(kvMap))
	ck.putCmdSeqNo++
	args := PutAppendArgs{kvMap, ck.myID, ck.putCmdSeqNo}

	for {
		if ck.leaderID != -1 {
			reply := PutAppendReply{}
			if ck.servers[ck.leaderID].Call("KVServer.PutAppend", &args, &reply) &&
				reply.Err == OK {
				for key := range kvMap {
					ck.logger.Printf("[PUT] {%v} Unlock key %v\n", strconv.FormatInt(ck.myID, 10)[:4], key)
					ck.lockManager.Unlock(key)
					delete(ck.kvMap, key)
				}
				ck.logger.Printf("[PUT] {%v} Leader %v returns %+v\n\n", strconv.FormatInt(ck.myID, 10)[:4], ck.leaderID, reply)
				return
			}
			ck.leaderID = -1
		}

		tryServer := nrand() % int64(len(ck.servers))
		reply := PutAppendReply{}
		if ck.servers[tryServer].Call("KVServer.PutAppend", &args, &reply) &&
			reply.Err == OK {
			ck.leaderID = tryServer
			ck.logger.Printf("[PUT] {%v} Leader %v returns %+v\n", strconv.FormatInt(ck.myID, 10)[:4], tryServer, reply)
			for key := range kvMap {
				ck.logger.Printf("[PUT] {%v} Unlock key %v\n", strconv.FormatInt(ck.myID, 10)[:4], key)
				ck.lockManager.Unlock(key)
				delete(ck.kvMap, key)
			}
			ck.logger.Printf("[PUT] {%v} Leader %v returns %+v\n\n", strconv.FormatInt(ck.myID, 10)[:4], ck.leaderID, reply)
			return
		}
	}
}

func extractKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
