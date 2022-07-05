# FOREWORD

This is **my project report** for [MIT's 6.824 Distributed Systems, 2022 Spring](http://nil.lcs.mit.edu/6.824/2022/schedule.html).

**PLEASE NOTE:** The hyperlinks to my **source code** in this repo are **INVALID!!!** This is a public version of my project. I don't open my source code because it is a course project and I believe I'm obliged to help protect academic integrity.

# ENVIRONMENT SETUP

The whole lab is developed under `Go`, so you should [set up the `Go` environment](http://nil.lcs.mit.edu/6.824/2022/labs/go.html) before testing my code. 

# Project Overview

## Lab 1 - MapReduce

[Here](http://nil.lcs.mit.edu/6.824/2022/labs/lab-mr.html) is the original lab requirement.

### Goal

Implement the coordinator and the worker according to [the MapReduce paper](http://nil.lcs.mit.edu/6.824/2022/papers/mapreduce.pdf). *This Lab Implemented a __Shared-FileSystem__ MapReduce.*

The workers will contact the coordinator for a task, then do the task.

The coordinator should distribute tasks to workers, but it has to ensure that:

1. The final results should be correct, although one file could be sent to multiple workers;
2. Handle crashed, or slow workers. Redistribute their tasks to other available workers.

I use the `channel` to solve the concurrency problem in coordinators. So my implementation is **LOCK-FREE**!

### Testing

I ran the [test script](src/main/test-mr.sh) 500 times **WITHOUT FAILURE**. The output of my run of test can be found at [docs/test_logs/test-mr.log.txt](docs/test_logs/test-mr.log.txt).

To verify it, make sure you are under `src/main/`, then run:

```bash
$ bash test-mr-many.sh 500
```

*A hint*: You'd better run it on some servers with the command:

```bash
$ nohup bash test-mr-many.sh 500
```

Because each test round will cost approximately 80 seconds, completing the 500 rounds of tests takes HOURS!

### Detailed Report & Relevant Code

The detailed report can be found in [`docs/lab1.md`](docs/lab1.md).

The relevant code files are:

- `src/mr/coordinator.go`
- `src/mr/rpc.go`
- `src/mr/worker.go`

## Lab 2 - Raft

[Here](http://nil.lcs.mit.edu/6.824/2021/labs/lab-raft.html) is the original lab requirement.

I'm going to implement the `Raft` peers according to [this paper](http://nil.lcs.mit.edu/6.824/2021/papers/raft-extended.pdf) so that they can form a fault-tolerant distributed system.

### Part A: Leader Election

#### Goal

Implement the following functions of a Raft peer so that a group of it can elect a leader:

- `ticker()`: Periodically check whether the peer should launch a new round of elections or not. If it did not receive heartbeats from the current leader for a while(500ms), it will increase its `currentTerm` and send `RequestVote` to all its peers in parallel to get elected as the new leader.
- `RequestVote()`: The RPC stub that provides vote service to other peers. 
- `AppendEntries()`: The RPC stub that is exposed for a leader to call. It serves as heartbeats to prevent followers from starting elections.
- `heartBeat()`: When a peer is elected as the leader, it should periodically(100ms) send heartbeats to all of its peers. If more than half of its peers do not respond to its heartbeat(RPC fail), it will realize that there's no majority, so it should convert to a follower and start election until a new leader appears.

#### Testing

Under `6.824/src/raft`, run the following command:

```bash
$ go test -run 2A -race
```

It will run the three tests designed for this part. 

**I RAN THE TESTS FOR 500 TIMES WITHOUT FAILURE!** The output of my run of test can be found at [docs/test_logs/test2A.log.txt](docs/test_logs/test2A.log.txt).

For testing convenience, I provided the script [`test-many.sh`](scripts/test-many_lab2.sh)*(If you want to use it, please run it under `src/raft`)*, so now you can run the tests as:

```bash
$ bash test-many.sh 2A 500
```

It has the same effect as running the `Go` test 500 times.

#### Performance Compared To the Instructors' Solution

**Result**: There's no big difference between mine and the instructors'.

You can find the performance of MIT's solution on [the lab specification page](http://nil.lcs.mit.edu/6.824/2022/labs/lab-raft.html) on the course website. And you can easily calculate my solution's average performance by [processing](scripts/calc_log_lab2.py) [the test logs](docs/test_logs/).

**We both run the tests WITHOUT `-race`!**

##### My *Average* Performance

| Test Name             | Time | Servers | RPC Count | Total Bytes | Commits |
| --------------------- | ---- | ------- | --------- | ----------- | ------- |
| TestInitialElection2A | 3.3  | 3       | 53        | 14272       | 0       |
| TestReElection2A      | 5.5  | 3       | 103       | 21494       | 0       |
| TestManyElections2A   | 7.2  | 7       | 545       | 112782      | 0       |

##### Instructors' Performance

| Test Name             | Time | Servers | RPC Count | Total Bytes | Commits |
| --------------------- | ---- | ------- | --------- | ----------- | ------- |
| TestInitialElection2A | 3.5  | 3       | 58        | 16840       | 0       |
| TestReElection2A      | 5.4  | 3       | 118       | 25269       | 0       |
| TestManyElections2A   | 7.3  | 7       | 624       | 138014      | 0       |

### Part B: Log Replication

#### Goal

Amend the behaviors of leaders and followers so that the system can reach a consensus on their logs. Specifically, the following functions in *Part A* should be amended:

- `RequestVote()`: The follower should **deny** a vote request if the candidate's log is NOT as up-to-date as the follower.

- `AppendEntries()`: The follower should **remove** conflicting logs, **append** new log entries from the leader, and **commit** any logs that the leader committed. If the follower falls behind the leader (so it cannot append new logs), it should **notify** the leader of its failure so that the leader will send older logs for it to update.

- `heartBeat()`: The leader should prepare heartbeat messages for every peer according to their `nextIndex[]`.

  `nextIndex[i]` records where the leader expects the next log should appear on peer `i`. If the leader has longer logs than `nextIndex[]`, it should add these new logs into heartbeat messages. 

  If a follower fails to append these new logs, the leader should decrease the follower's `nextIndex` by 1 so that in the next round of heartbeats, the leader will add more old logs into heartbeat messages.

#### Testing

Under `6.824/src/raft`, run the following command:

```bash
$ go test -run 2B -race
```

It will run the tests designed for this part. 

**I RAN THE TESTS FOR 500 TIMES WITHOUT FAILURE!** The output of my run of test can be found at [docs/test_logs/test2B.log.txt](docs/test_logs/test2B.log.txt).

#### Performance Compared To the Instructors' Solution

**Results**: Under complex situations, my solution consumes **half** the traffic compared to the instructors'.

You can find the performance of MIT's solution on [the lab specification page](http://nil.lcs.mit.edu/6.824/2022/labs/lab-raft.html) on the course website. And you can easily calculate my solution's average performance by [processing](scripts/calc_log_lab2.py) [the test logs](docs/test_logs/).

**We both run the tests WITHOUT `-race`!**

##### My *Average* Performance

| Test Name              | Time | Servers | RPC Count | Total Bytes | Commits |
| ---------------------- | ---- | ------- | --------- | ----------- | ------- |
| TestBasicAgree2B       | 1.3  | 3       | 17        | 4627        | 3       |
| TestRPCBytes2B         | 3.3  | 3       | 53        | 115110      | 11      |
| TestFailAgree2B        | 5.5  | 3       | 90        | 23068       | 7       |
| TestFailNoAgree2B      | 4.3  | 5       | 157       | 35061       | 3       |
| TestConcurrentStarts2B | 1.1  | 3       | **12**    | **3458**    | 6       |
| TestRejoin2B           | 5.4  | 3       | 135       | 32158       | 4       |
| TestBackup2B           | 28.9 | 5       | **1359**  | **777127**  | 102     |
| TestCount2B            | 2.6  | 3       | **39**    | **11122**   | 12      |

##### Instructors' Performance

| Test Name              | Time | Servers | RPC Count | Total Bytes | Commits |
| ---------------------- | ---- | ------- | --------- | ----------- | ------- |
| TestBasicAgree2B       | 0.9  | 3       | 16        | 4572        | 3       |
| TestRPCBytes2B         | 1.7  | 3       | 48        | 114536      | 11      |
| TestFailAgree2B        | 3.6  | 3       | 78        | 22131       | 7       |
| TestFailNoAgree2B      | 3.8  | 5       | 172       | 40935       | 3       |
| TestConcurrentStarts2B | 1.1  | 3       | **24**    | **7379**    | 6       |
| TestRejoin2B           | 5.1  | 3       | 152       | 37021       | 4       |
| TestBackup2B           | 17.2 | 5       | **2080**  | **1587388** | 102     |
| TestCount2B            | 2.2  | 3       | **60**    | **20119**   | 12      |

### Part C: Persistence

#### Goal

Add features to **persist** the raft's state so the system can survive *even if the peers crash*.

The tasks are:

- `persist()`: Use the `persister` to encode the persistent states described in the paper. 
- `readPersist()`: When we `Make` a new raft, it should read persistent data from the `persister` if we provide it. 
- Call `persist()` on every persistent state change.

**The main difficulty of this lab is DEBUGGING.** The potential bugs in Part A and B will occur in this lab.

#### Testing

Under `6.824/src/raft`, run the following command:

```bash
$ go test -run 2C -race
```

It will run the tests designed for this part. 

**I RAN THE TESTS FOR 500 TIMES WITHOUT FAILURE!** The output of my run of test can be found at [docs/test_logs/test2C.log.txt](docs/test_logs/test2C.log.txt).

#### Performance Compared To the Instructors' Solution

You can find the performance of MIT's solution on [the lab specification page](http://nil.lcs.mit.edu/6.824/2022/labs/lab-raft.html) on the course website. And you can easily calculate my solution's average performance by [processing](scripts/calc_log_lab2.py) [the test logs](docs/test_logs/).

**We both run the tests WITHOUT `-race`!**

##### My *Average* Performance

| Test Name               | Time | Servers | RPC Count | Total Bytes | Commits |
| ----------------------- | ---- | ------- | --------- | ----------- | ------- |
| TestPersist12C          | 5.2  | 3       | 85        | 21211       | 6       |
| TestPersist22C          | 19   | 5       | 848       | 187899      | 16      |
| TestPersist32C          | 2.6  | 3       | 35        | 8859        | 4       |
| TestFigure82C           | 32.2 | 5       | 629       | 127248      | 15      |
| TestUnreliableAgree2C   | 14.7 | 5       | 489       | 147422      | 246     |
| TestFigure8Unreliable2C | 34.6 | 5       | 1054      | 672912      | 199     |
| TestReliableChurn2C     | 16.4 | 5       | 556       | 239071      | 123     |
| TestUnreliableChurn2C   | 16.5 | 5       | 470       | 150375      | 92      |

##### Instructors' Performance

| Test Name               | Time | Servers | RPC Count | Total Bytes | Commits |
| ----------------------- | ---- | ------- | --------- | ----------- | ------- |
| TestPersist12C          | 5    | 3       | 86        | 22849       | 6       |
| TestPersist22C          | 17.6 | 5       | 952       | 218854      | 16      |
| TestPersist32C          | 2    | 3       | 34        | 8937        | 4       |
| TestFigure82C           | 31.2 | 5       | 580       | 130675      | 32      |
| TestUnreliableAgree2C   | 1.7  | 5       | 1044      | 366392      | 246     |
| TestFigure8Unreliable2C | 33.6 | 5       | 10700     | 33695245    | 308     |
| TestReliableChurn2C     | 16.1 | 5       | 8864      | 44771259    | 1544    |
| TestUnreliableChurn2C   | 16.5 | 5       | 4220      | 6414632     | 906     |

### Part D: Log Compaction

#### Goal

The log of a raft cannot accumulate infinitely. Thus, we have to trim the log by taking snapshots.

Complete the `Raft` by adding the following functions:

- `Snapshot(index int, snapshot []byte)`: This is an **API** called by the upper service. It notifies the raft that the service has taken a snapshot before the `index`, and the raft should persist the `snapshot`. Thus, the raft will delete all logs before the `index`.

- `InstallSnapshot()`: This is an RPC stub for the leader to call. When a peer falls behind the leader so much that the leader no longer stores the old logs needed by `AppendEntries`, the leader will send its snapshot to the follower through this RPC. Therefore, the follower can immediately catch up with the leader after installing the leader's snapshot.

  Be careful that this RPC should refuse old snapshots. States can **only** go forward, not backward.

  Another problem is the coordination with the `applier` thread, which applies uncommitted changes whenever there're any. While we are installing a snapshot, the `applier` should NOT work. I solve this problem by setting the `lastApplied` to the index where the raft should be after installing this snapshot.

#### Testing

Under `6.824/src/raft`, run the following command:

```bash
$ go test -run 2D -race
```

It will run the tests designed for this part. 

**I RAN THE TESTS FOR 500 TIMES WITHOUT FAILURE!** The output of my run of test can be found at [docs/test_logs/test2D.log.txt](docs/test_logs/test2D.log.txt).

#### Performance Compared To the Instructors' Solution

You can find the performance of MIT's solution on [the lab specification page](http://nil.lcs.mit.edu/6.824/2022/labs/lab-raft.html) on the course website. And you can easily calculate my solution's average performance by [processing](scripts/calc_log_lab2.py) [the test logs](docs/test_logs/).

**We both run the tests WITHOUT `-race`!**

##### My *Average* Performance

| Test Name                       | Time | Servers | RPC Count | Total Bytes | Commits |
| ------------------------------- | ---- | ------- | --------- | ----------- | ------- |
| TestSnapshotBasic2D             | 8.8  | 3       | 153       | 52833       | 220     |
| TestSnapshotInstall2D           | 61.2 | 3       | 1190      | 428944      | 328     |
| TestSnapshotInstallUnreliable2D | 80.1 | 3       | 1317      | 465028      | 324     |
| TestSnapshotInstallCrash2D      | 42.9 | 3       | 695       | 301302      | 326     |
| TestSnapshotInstallUnCrash2D    | 63.7 | 3       | 884       | 355904      | 325     |
| TestSnapshotAllCrash2D          | 17.8 | 3       | 287       | 80669       | 58      |

##### Instructors' Performance

| Test Name                       | Time | Servers | RPC Count | Total Bytes | Commits |
| ------------------------------- | ---- | ------- | --------- | ----------- | ------- |
| TestSnapshotBasic2D             | 11.6 | 3       | 176       | 61716       | 192     |
| TestSnapshotInstall2D           | 64.2 | 3       | 878       | 320610      | 336     |
| TestSnapshotInstallUnreliable2D | 81.1 | 3       | 1059      | 375850      | 341     |
| TestSnapshotInstallCrash2D      | 53.5 | 3       | 601       | 256638      | 339     |
| TestSnapshotInstallUnCrash2D    | 63.5 | 3       | 687       | 288294      | 336     |
| TestSnapshotAllCrash2D          | 19.5 | 3       | 268       | 81352       | 58      |

### Detailed Report & Relevant Code

**I designed some unique features to polish and optimize the Raft!** 

**The detailed report can be found in [`docs/lab2.md`](docs/lab2.md).**

The relevant code file(s) are:

- `src/raft/raft.go`
