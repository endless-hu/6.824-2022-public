# Explore Project: KV Raft Based On Frangipani

The idea of this project originated from the testing requirements in lab 3 - KV Raft.

**WARN: this directory should be located under `6.824/src/`. To test the code, you MUST implement the code in `6.824/raft` first!!!**

**NOTE: I _limited_ the performance of frangipani by adding a `time.Sleep(5*time.Millisecond)` before a client executing `PutAppend`.** Otherwise, the system will execute too many `PutAppend`s, consuming all memory and forcing the OS to abort the testing program.

TO unleash the full power of frangipani, please comment the `time.Sleep` statement in `client.go:105`.

Please refer to [`6.824/docs/frangipani`](../docs/frangipani.md) for detailed report.