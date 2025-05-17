# DB comparison

This is a comparison between [`boltdb/bolt`](https://github.com/boltdb/bolt) and [`five-vee/byodb-rust`](https://github.com/five-vee/byodb-rust) ("build your own DB in Rust").

[`boltdb/bolt`](https://github.com/boltdb/bolt) is an embedded key/value store inspired by [Howard Chu's LMDB project](http://symas.com/mdb/) and written in Golang.

[`five-vee/byodb-rust`](https://github.com/five-vee/byodb-rust) is my personal DB created as a learning project. It is modeled after https://build-your-own.org/database/.

## Similarities

* Embedded key/value store
* ACID transactions
* MVCC support:

Both are essentially inspired by LMDB. They're both implemented as copy-on-write B+ trees, are backed by memory-mapped file, and have file page re-use via a page free list.

## Differences

`boltdb/bolt` | `five-vee/byodb-rust`
--- | ---
written in Golang | written in Rust
battle-tested (see the [`etcd-io/bbolt`](https://github.com/etcd-io/bbolt) fork) | not production ready (just a learning project)

## Benchmarks

The following is a benchmark run where there are `4` parallel readers and `1` parallel writer (that spins but does nothing useful). The underlying DB is seeded with `40000` items first before running the benchmark.

```shell
$ items=40000
$ threads=4
$ iters=1000
$ background_writer=true
$ cd byodb-rust
$ cargo run --profile=release $items $threads $iters $background_writer
n_items: 40000, n_threads: 4, n_iters: 1000, bkgd_writer: true, elapsed: 952171us
Avg latency per item: 0.024us
$ cd ../boltdb-go
$ go run main.go $items $threads $iters $background_writer
items: 40000, threads: 4, iters: 1000, backgroundWriter: true, elapsed: 667842us
Avg latency per item: 0.017us
```

As can be seen, `byodb-rust` is slightly behind `boltdb` in terms of latency (`0.024us` vs `0.017us`). A superficial assumption would be that performance should be better in Rust vs in Go. But in this case this is not true, even though I took advantage of many performance benefits that are in Rust and not in Go, including (but not limited to):

* manual memory management
* explicit inlining

Although it is difficult to pinpoint why the Rust version isn't much faster, my best guess is that reading from memory is the bottleneck, as the DB was not CPU bound (ignoring disk IO b/c for now the DB fits completely in memory). And like `byodb-rust`, `boltdb` also utilizes a memory-map, meaning little heap-usage. So GC pressure isn't particularly high either.

This was a learning project, after all. I followed https://build-your-own.org/database/, which did not have the best and optimal memory representation of B+ tree pages. I did try to add some optimizations, but they ultimately were premature and did not add much to performance. Chalk this one up to me getting too deep into the rabbit-hole of learning both a new language (Rust) and how B+ tree databases worked. 😛

If I were to redo this project again, using my learnings, I would probably research more optimal tree node formats first from DBs such as LMDB, SQLite/Turso, and CedarDB.