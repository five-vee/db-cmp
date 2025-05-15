package main

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/boltdb/bolt"
)

const (
	bucketName     = "MyBucket"
	maxKeyLength   = 1000
	maxValueLength = 1000
)

func main() {
	items := 1000
	threads := 1
	iters := 1000
	backgroundWriter := true
	if len(os.Args) == 1 {
	} else if len(os.Args) == 5 {
		var err error
		if items, err = strconv.Atoi(os.Args[1]); err != nil {
			log.Fatalf("items is not an integer: %v", err)
		}
		if threads, err = strconv.Atoi(os.Args[2]); err != nil {
			log.Fatalf("threads is not an integer: %v", err)
		}
		if iters, err = strconv.Atoi(os.Args[3]); err != nil {
			log.Fatalf("iters is not an integer: %v", err)
		}
		if backgroundWriter, err = strconv.ParseBool(os.Args[4]); err != nil {
			log.Fatalf("backgroundWriter is not an integer: %v", err)
		}
	} else {
		fmt.Printf("Usage: %s <items> <threads> <iters> <backgroundWriter>\n", os.Args[0])
		os.Exit(1) // Exit with a non-zero status code to indicate an error
	}

	elapsed := benchmarkReaders(items, threads, iters, backgroundWriter)
	fmt.Printf("items: %d, threads: %d, iters: %d, backgroundWriter: %t, elapsed: %dus\n",
		items, threads, iters, backgroundWriter, elapsed.Microseconds())
	fmt.Printf("Avg latency per item: %.3fus\n", float64(elapsed.Microseconds())/float64(iters*items))
}

// randomAlphanumericString generates a random string of the specified length
// containing only alphanumeric characters.
func randomAlphanumericString(length int) string {
	const alphanumericCharset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = alphanumericCharset[rand.Intn(len(alphanumericCharset))]
	}
	return string(b)
}

// mustFileExists checks if a file or directory exists at the given path.
func mustFileExists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	// Another error occurred (e.g., permission denied)
	log.Fatalf("error checking file existence: %v", err)
	return false // unreachable
}

// mustRandomTemporaryFile creates a temporary file with a random name.
// The file is created in the directory specified by the TMPDIR environment variable.
// If TMPDIR is not set, the program will exit with an error.
// The file name will be a random alphanumeric string of the specified length.
// The function will retry generating a file name until a non-existent file is found.
// It returns the path to the created file.
func mustRandomTemporaryFile(fileNameLength int) string {
	tmpDir, ok := os.LookupEnv("TMPDIR")
	if !ok {
		log.Fatalf("This benchmark can only be run on Mac OS X (no $TMPDIR found).")
	}
	tmpPath := path.Join(tmpDir, randomAlphanumericString(fileNameLength))
	for mustFileExists(tmpPath) {
		tmpPath = path.Join(tmpDir, randomAlphanumericString(fileNameLength))
	}
	return tmpPath
}

// tryRemoveIfExists attempts to remove the file if it exists.
func tryRemoveIfExists(filePath string) {
	if !mustFileExists(filePath) {
		return
	}
	if err := os.Remove(filePath); err != nil {
		log.Printf("error removing file: %v", err)
	}
}

// setupDB creates a new BoltDB database for testing.
// The cleanup function should be deferred immediately to close the database
// and remove the temporary file.
// If any error occurs during database creation, the program will exit with a
// fatal error.
func setupDB() (db *bolt.DB, cleanup func()) {
	// Setup temporary file and open database.
	tmpPath := mustRandomTemporaryFile(10)
	db, err := bolt.Open(tmpPath, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		tryRemoveIfExists(tmpPath)
		log.Fatalf("failed to open bolt DB: %v", err)
	}
	// b.Logf("Opened BoltDB at temporary file: %s\n", tmpPath)
	cleanup = func() {
		tryRemoveIfExists(tmpPath) // b is captured from setupDB's scope
		db.Close()
	}
	return db, cleanup
}

// mustSeedDB seeds the database with a specified number of key-value pairs.
// It creates a bucket and inserts random alphanumeric strings as keys and
// values.
func mustSeedDB(db *bolt.DB, n int) {
	err := db.Batch(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucket([]byte(bucketName))
		if err != nil {
			return fmt.Errorf("failed to create bucket: %w", err)
		}
		for i := 0; i < n; i++ {
			keyLen := rand.Intn(maxKeyLength) + 1
			valLen := rand.Intn(maxValueLength) + 1
			key := []byte(randomAlphanumericString(keyLen))
			val := []byte(randomAlphanumericString(valLen))
			if err := b.Put(key, val); err != nil {
				return fmt.Errorf("failed to Put key i=%d: %w", i, err)
			}
		}
		return nil
	})
	if err != nil {
		log.Fatalf("Failed to seed DB: %v", err)
	}
}

func benchmarkReaders(items, threads, iters int, backgroundWriter bool) time.Duration {
	// Setup.
	db, cleanup := setupDB()
	defer cleanup()
	mustSeedDB(db, items)

	// Optionally start background writer.
	stop := make(chan struct{})
	if backgroundWriter {
		go func() {
			runtime.LockOSThread()
			// defer runtime.UnlockOSThread()
			err := db.Batch(func(tx *bolt.Tx) error {
				b := tx.Bucket([]byte(bucketName))
				for {
					select {
					case <-stop:
						return nil
					default:
						if err := b.Put([]byte("dummy_key"), []byte("dummy_val")); err != nil {
							return err
						}
					}
				}
			})
			if err != nil {
				panic(fmt.Errorf("failed to Batch into DB: %w", err))
			}
		}()
	}

	// Run benchmark load.
	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(threads)
	for range threads {
		go func() {
			defer wg.Done()
			runtime.LockOSThread()
			// defer runtime.UnlockOSThread()
			for range iters {
				err := db.View(func(tx *bolt.Tx) error {
					c := tx.Bucket([]byte(bucketName)).Cursor()
					for k, v := c.First(); k != nil; k, v = c.Next() {
						_ = k
						_ = v
					}
					return nil
				})
				if err != nil {
					panic(fmt.Errorf("failed to view into DB: %w", err))
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Now().Sub(start)
	close(stop)
	return elapsed
}
