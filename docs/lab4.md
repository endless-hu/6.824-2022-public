# Lab 4: ShardKV

[Here](http://nil.lcs.mit.edu/6.824/2022/labs/lab-shard.html) is the original lab requirement.

## Goal

Build a key-value storage system that *partitions* the key over a set of replica groups. 

A *shard* is a subset of the total key-value pairs, and each replica group handles multiple shards. The distribution of shards should be balanced so that the system's throughput can be maximized. 

## Lab 4A: Shard Controller

### Basic Idea

We can directly modify lab3's code to implement the shard controller. They share the same structure. The only difference is that the shard controller should maintain a slice of configs instead of a key-value map.

**NOTE**: The `slice` and `map ` in Go are *references*. Simple assignment `=` will make the variable points to the same map or slice. Therefore, when appending a modified config to the config slice

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