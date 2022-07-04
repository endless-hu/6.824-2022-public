# Lab 1 - MapReduce

[Here](http://nil.lcs.mit.edu/6.824/2022/labs/lab-mr.html) is the original lab requirement.

## Goal

Implement the coordinator and the worker according to [the paper](http://nil.lcs.mit.edu/6.824/2022/papers/mapreduce.pdf). 

### For Worker

The workers will:

1. Contact the coordinator for a task file.
2. `map` the file into many intermediate key-value pairs. (OR, `reduce` all intermediate files in the same bucket)
3. Distribute these intermediate key-value pairs into `nReduce` intermediate files. (OR, produce the `reduce`d result into a final `mr-out-*` file.)
4. Report back to the coordinator.

The workers are sequential, no need to consider race conditions.

### For Coordinator

The coordinator should ensure:

1. Every file should be processed **ONLY ONCE**, otherwise the final result will be incorrect.
2. Handle crashed, or slow workers. Redistribute their tasks to other available workers.

Because the RPC stub, `HeartBeat(args *HeartBeatClient, reply *HeartBeatMaster)` introduces concurrent situations to the coordinator, the race conditions must be effectively addressed. 

I use the `channel` to solve the problem. So my implementation is **LOCK-FREE**!

## Implementation

### Workers

A worker does NOT have a data structure. It only communicates with the coordinator by heartbeat messages. In every heartbeat message, it:

1. Reports to the coordinator what it has done since the last heartbeat exchange. 
2. Gets a task from the coordinator. Possible tasks are:
   1. `MAP` task with the file's name and `fileID`. So the worker just applies the `map` function to that file and distributes the results evenly into `nReduce` files using a hash function. It will produce a sequence of files named `mr-${fileID}-${partnum}`. The `partnum` ranges in $[0,nReduce)$
   2. `REDUCE` task with the target bucket number. The worker will glean the intermediate key-value pairs from all files named `mr-*-${partnum}` and apply `reduce` to these pairs.
   3. `SLEEP` task. In this case, the coordinator currently has no tasks to distribute, so the worker will sleep for 1 second and start heartbeat again.

### Coordinators

```go
type Coordinator struct {
  // constants, initialized at the beginning
	nReduce int
	nFiles  int

  // Channels used to communicated between main thread and the RPC
	doneCh   chan struct{}
	taskCh   chan Msg
	reportCh chan Msg

  // all files' status
	files             []*fileStat
	undistributed_idx int
	distributed_files map[int]*fileStat

  // all parts' status
	parts             []*partStat
	distributed_parts map[int]*partStat

  // helper variables to keep track of the coordinator status
	inMapPhase bool
	map_cnt    int
	reduce_cnt int
}
```

#### When we `makeCoordinator()`

The main thread named `c.master()` will be launched to monitor the `reportCh`. 

All modifications to the `Coordinator` data structure will be applied in the `master()` thread, thus avoiding locks.

#### When a worker invokes the RPC handler of the coordinator

1. The RPC handler will send a message through the `reportCh`. 
2. The message will be captured by the `c.master()` thread.
3. `c.master()` will respond with a task, which is sent through `taskCh`.
4. The handler captured the task and reply to the worker.

```go
func (c *Coordinator) HeartBeat(args *HeartBeatClient, reply *HeartBeatMaster) error {
	c.reportCh <- Msg{args.JobType, args.FileID_OR_PartNum}
	msg := <-c.taskCh
	reply.NReduce = c.nReduce
	reply.Nfiles = c.nFiles

	reply.JobType = msg.job_type
	reply.FileID_OR_Partnum = msg.fileID_OR_partnum
	if msg.job_type == MAP {
		reply.FileName = c.files[msg.fileID_OR_partnum].filename
	}
	return nil
}
```

#### When we query `Done()`

It will try to read from `doneCh`. If nothing can be read, then the whole work has not been done yet, and it will return `false`. Else return `true`.

#### The Procedure of `c.master()`

The pseudo-code is

```
func (c *Coordinator) master() {
	for {
		msg := <-c.reportCh
		IF all jobs are done, THEN send an EXIT task and return
		
		switch msg.job_type {
		case MAP_DONE:
			IF msg.file has NOT been mapped, THEN mark it as "mapped"
			IF all files are mapped, THEN 
				reset c.undistributed_idx to 0 
				and flip c.inMapPhase
		case REDUCE_DONE:
			IF msg.part has NOT been reduced, THEN mark it as "reduced"
			IF all parts are reduced, THEN launch a thread to send messages to c.doneCh
		}
		// Find a task and send it to c.taskCh
		c.allocateTask()
	}
}
```

The pseudo-code of allocating a task is:

```
func (c *Coordinator) allocateTask() {
	if c.inMapPhase {
		if (there're files undistributed) {
			c.taskCh <- Msg{MAP, c.files[c.undistributed_idx].fileID}
			record its distribution time (time.Now())
			add it to c.distributed_files
			return
		} else {
			find a timed-out file in c.distributed_files
			refresh its distribution time
			return
		}
	} else {
		if (there're parts undistributed) {
			c.taskCh <- Msg{REDUCE, c.parts[c.undistributed_idx].partnum}
			record its distribution time (time.Now())
			add it to c.distributed_files
			return
		} else {
			find a timed-out part in c.distributed_parts
			refresh its distribution time
			return
		}
	}
	// No task, let worker sleep
	c.taskCh <- Msg{SLEEP, -1}
}
```

## Design Subtleties - The way to achieve correctness

### One Deterministic Thing

Each file will be mapped into `nReudce` parts, and will finally be reduced into one `mr-out-${fileID}` result. 

### How To Utilize It - From the Worker's Perspective

The workers have to ensure that an output file in its formal name should be complete. For example, if a worker produced the output file, `mr-out-0`, then it must represent the correct result of the file 0.

To avoid partial writing to a file, the worker will first create a temp file, by adding random prefixes to the file name. When all writings are finished, it renames the temp file to its formal name and notifies the coordinator of the completion of the task.

**If the coordinator hands out the same file to multiple workers at the same time, the result is STILL CORRECT.** For example, the coordinator handed out the file whose ID is 0 to two workers. The two workers will execute the `map` function on the file *independently*, and create their own temp files. Say the first worker creates a temp file named `mr-0-08514463021`, and the second creates `mr-0-074320125630`. They write the intermediate results into the two temp files, respectively. 

Finally, they will rename their own temp files into the formal files, i.e.`mr-0-0`. Only one will succeed because the OS does NOT allow files with the same name under the same directory. Therefore, the worker will detect the rename error and delete the file that failed to be renamed, so the future `reduce` will NOT read the temp file. 

## Performance Improvement

*Please switch to the branch `mropt` if you want to read the source code and test.*

According to [the former discussion](#How To Utilize It - From the Worker's Perspective), we can find out that the coordinator **DO NOT** need to wait for time out if it wants to *redistribute* a task. In fact, it can redistribute tasks *at any time*, EVEN IF there are multiple workers working on the same task. 

Therefore, whenever a worker requests a task from the coordinator, if all tasks have been distributed currently, the coordinator can randomly pick a distributed task to reply to the worker, instead of letting the worker sleep. Thus, the coordinator can get a task done as soon as it receives the report from the fastest worker. 

In my real testing, this little trick can reduce the test time from 80 seconds to 70 seconds. 

**But,** the test script includes a job count test, which examine whether a task was handed out multiple times when there's no worker failure. Therefore, the optimized version sometimes CANNOT pass the test script. But it is still CORRECT!
