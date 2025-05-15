use std::env;
use std::process;
use std::rc::Rc;
use std::sync::Arc;
use std::sync::mpsc::{self, Receiver, Sender};
use std::thread;
use std::time::{Duration, Instant};

use anyhow::{Context, Result};
use rand::{
    SeedableRng,
    distr::{Alphabetic, SampleString},
    prelude::*,
};
use rand_chacha::ChaCha8Rng;
use tempfile::NamedTempFile;

use byodb_rust::{
    DB, DBBuilder, consts,
    error::{NodeError, TreeError, TxnError},
};

const DEFAULT_SEED: u64 = 1;

fn main() {
    let args: Vec<String> = env::args().collect();

    let mut n_items = 1000;
    let mut n_threads = 1;
    let mut n_iters = 1000;
    let mut bkgd_writer = true;
    if args.len() == 1 {
    } else if args.len() == 5 {
        n_items = args[1].parse::<usize>().expect("n_items is a usize");
        n_threads = args[2].parse::<usize>().expect("n_threads is a usize");
        n_iters = args[3].parse::<usize>().expect("n_iters is a usize");
        bkgd_writer = args[4].parse::<bool>().expect("bkgd_writer is a bool")
    } else {
        println!(
            "Usage: {} <n_items> <n_threads> <n_iters> <bkgd_writer>",
            args[0]
        );
        process::exit(1); // Exit with an error code
    }

    let elapsed = bench_readers(n_items, n_threads, n_iters, bkgd_writer);
    println!(
        "n_items: {n_items}, n_threads: {n_threads}, n_iters: {n_iters}, bkgd_writer: {bkgd_writer}, elapsed: {}us",
        elapsed.as_micros()
    );
    println!(
        "Avg latency per item: {:.3}us",
        elapsed.as_micros() as f64 / (n_iters * n_items) as f64
    );
}

fn new_test_db() -> (DB, NamedTempFile) {
    let temp_file = NamedTempFile::new().unwrap();
    let path = temp_file.path();
    let db = DBBuilder::new(path).build().unwrap();
    (db, temp_file)
}

struct Seeder {
    n: usize,
    rng: ChaCha8Rng,
}

impl Seeder {
    fn new(n: usize, seed: u64) -> Self {
        Seeder {
            n,
            rng: ChaCha8Rng::seed_from_u64(seed),
        }
    }

    fn seed_db(self, db: &DB) -> Result<()> {
        let mut t = db.rw_txn();
        for (i, (k, v)) in self.enumerate() {
            let result = t.insert(k.as_bytes(), v.as_bytes());
            if matches!(
                result,
                Err(TxnError::Tree(TreeError::Node(NodeError::AlreadyExists)))
            ) {
                // Skip
                continue;
            }
            result.with_context(|| format!("failed to insert {i}th ({k}, {v})"))?;
        }
        t.commit();
        Ok(())
    }
}

impl Iterator for Seeder {
    type Item = (String, String);
    fn next(&mut self) -> Option<Self::Item> {
        if self.n == 0 {
            return None;
        }
        self.n -= 1;
        let key_len = self.rng.random_range(1..=consts::MAX_KEY_SIZE);
        let val_len = self.rng.random_range(1..=consts::MAX_VALUE_SIZE);
        let key: String = Alphabetic.sample_string(&mut self.rng, key_len);
        let val: String = Alphabetic.sample_string(&mut self.rng, val_len);
        Some((key, val))
    }
}

fn bench_readers(n_items: usize, n_threads: usize, n_iters: usize, bkgd_writer: bool) -> Duration {
    // Setup.
    let (db, _temp_file) = new_test_db();
    let db = Arc::new(db);
    Seeder::new(n_items, DEFAULT_SEED).seed_db(&db).unwrap();

    // Optionally start background writer.
    let (sender, receiver): (Sender<()>, Receiver<()>) = mpsc::channel();
    let background_thread = if bkgd_writer {
        Some(thread::spawn({
            let db = db.clone();
            move || {
                let mut t = db.rw_txn();
                // Get one key.
                let (k, _) = t.in_order_iter().next().unwrap();
                let k: Rc<[u8]> = k.into();
                let dummy_val = [1u8; 100];
                // Mindlessly do some busy work until termination.
                while receiver.try_recv().is_err() {
                    t.update(&k, &dummy_val).unwrap();
                }
                t.abort();
            }
        }))
    } else {
        None
    };

    // Run benchmark load.
    let start_time = Instant::now();
    let mut threads = Vec::new();
    for _ in 0..n_threads {
        let db = db.clone();
        threads.push(thread::spawn(move || {
            for _ in 0..n_iters {
                let t = db.r_txn();
                for (_k, _v) in t.in_order_iter() {}
            }
        }));
    }
    for thread in threads {
        thread.join().unwrap();
    }
    let elapsed = start_time.elapsed();
    if let Some(background_thread) = background_thread {
        sender.send(()).unwrap();
        background_thread.join().unwrap();
    }
    elapsed
}
