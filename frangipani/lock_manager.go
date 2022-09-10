package frangipani

import (
	"log"
	"runtime/debug"
	"time"
)

/* Server side lock manger. */
type LockManagerServer struct {
	LockTable map[string]int64 // locked key -> clerkID
	CanRenew  map[string]bool
}

func (lm *LockManagerServer) Init() {
	lm.LockTable = make(map[string]int64)
	lm.CanRenew = make(map[string]bool)
}

func (lm *LockManagerServer) Lock(key string, clerkID int64) bool {
	if lockHolder, ok := lm.LockTable[key]; ok {
		if lockHolder != clerkID {
			lm.CanRenew[key] = false
		}
		return false
	}
	lm.LockTable[key] = clerkID
	lm.CanRenew[key] = true
	return true
}

func (lm *LockManagerServer) Unlock(key string) {
	delete(lm.LockTable, key)
	delete(lm.CanRenew, key)
}

func (lm *LockManagerServer) RenewLock(key string, clerkID int64) bool {
	if _, ok := lm.LockTable[key]; !ok {
		debug.PrintStack()
		log.Fatalf("[FATAL] key %s is not locked", key)
		return false
	}
	if lm.LockTable[key] != clerkID {
		debug.PrintStack()
		log.Fatalf("[FATAL] key %s is locked by another clerk %v\n", key, lm.LockTable[key])
		return false
	}
	return lm.CanRenew[key]
}

// Query the lock table to see if the key is locked by anyone.
// It will disable the renewing of the lock, because when called,
// there is another clerk who wants to lock the key.
func (lm *LockManagerServer) IsLocked(key string) bool {
	if _, ok := lm.LockTable[key]; !ok {
		return false
	}
	// disallow renewal because other clerk wants to lock the key
	lm.CanRenew[key] = false
	return true
}

// Query the lock table to see if the key is locked by the clerk.
// It will NOT disable the renewing of the lock.
func (lm *LockManagerServer) IsLockedBy(key string, clerkID int64) bool {
	if _, ok := lm.LockTable[key]; !ok {
		return false
	}
	return lm.LockTable[key] == clerkID
}

/*
Client side lock manager.
*/
type LockManagerClient struct {
	lockTable map[string]time.Time // locked key -> issued time
	locked    map[string]bool      // locked key -> true
}

func (lm *LockManagerClient) Init() {
	lm.lockTable = make(map[string]time.Time)
	lm.locked = make(map[string]bool)
}

func (lm *LockManagerClient) Lock(key string, issuedTime time.Time) {
	if locked, ok := lm.locked[key]; ok && locked {
		debug.PrintStack()
		log.Fatalf("[FATAL] key %s is already locked", key)
		return
	}
	lm.lockTable[key] = issuedTime
	lm.locked[key] = true
}

func (lm *LockManagerClient) LockRevoked(key string) bool {
	if _, ok := lm.locked[key]; !ok {
		return false
	}
	return !lm.locked[key]
}

func (lm *LockManagerClient) IsLocked(key string) bool {
	if _, ok := lm.lockTable[key]; !ok {
		return false
	}
	locked, ok := lm.locked[key]
	return ok && locked
}

func (lm *LockManagerClient) LockExpired(key string) bool {
	if _, ok := lm.locked[key]; !ok {
		return true
	}
	if locked, ok := lm.locked[key]; ok && locked {
		return time.Since(lm.lockTable[key]) >= leaseDuration
	}
	return true
}

func (lm *LockManagerClient) Unlock(key string) {
	delete(lm.lockTable, key)
	delete(lm.locked, key)
}

func (lm *LockManagerClient) RenewLock(key string, issuedTime time.Time) {
	if _, ok := lm.lockTable[key]; !ok {
		debug.PrintStack()
		log.Fatalf("[FATAL] key %s is not locked", key)
		return
	}
	// zero time means revoke the lock
	if issuedTime.IsZero() {
		lm.locked[key] = false
	}
	lm.lockTable[key] = issuedTime
}

func (lm *LockManagerClient) GetLockedKeySet() []string {
	var keys []string
	for key, locked := range lm.locked {
		if locked {
			keys = append(keys, key)
		}
	}
	return keys
}
