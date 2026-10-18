package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// groupSyncInterval batches fsyncs: an invoke waits at most this long to be
// durable.
const groupSyncInterval = 10 * time.Millisecond

const wallFormat = "2006-01-02T15:04:05.000Z07:00"

// ErrWriterClosed means a record arrived after the writer closed.
var ErrWriterClosed = errors.New("history writer is closed")

// Writer appends records to history.jsonl. Index and time are assigned under
// one lock, so file order, index order and time order agree.
type Writer struct {
	round   string
	started time.Time
	file    *os.File

	mu          sync.Mutex
	synced      *sync.Cond
	buffer      *bufio.Writer
	nextIndex   int64
	lastWritten int64
	lastSynced  int64
	err         error
	isClosed    bool

	stop    chan struct{}
	stopped chan struct{}
}

// Create opens a new history file; it refuses to overwrite an existing one.
func Create(path, round string) (*Writer, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: a path from its own config
	if err != nil {
		return nil, fmt.Errorf("creating history: %w", err)
	}

	writer := &Writer{
		round: round, started: time.Now(), file: file, buffer: bufio.NewWriter(file), nextIndex: 1, stop: make(chan struct{}), stopped: make(chan struct{}),
	}
	writer.synced = sync.NewCond(&writer.mu)

	go writer.syncLoop()

	return writer, nil
}

// Append writes a record without waiting for it to reach the disk. Completions
// use it: losing one in a crash only turns an operation into an open one.
func (w *Writer) Append(record Record) (Record, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.appendLocked(record)
}

// AppendDurable writes a record and returns only after it is on disk. Invokes
// use it, before the request leaves: a request the history does not know about
// would look like a phantom write to the checker.
func (w *Writer) AppendDurable(record Record) (Record, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	written, err := w.appendLocked(record)
	if err != nil {
		return Record{}, err
	}

	for w.lastSynced < written.Index && w.err == nil {
		w.synced.Wait()
	}

	if w.err != nil {
		return Record{}, w.err
	}

	return written, nil
}

// Elapsed is the monotonic time since the round started, as records show it.
func (w *Writer) Elapsed() time.Duration {
	return time.Since(w.started)
}

// Close syncs what is left and closes the file.
func (w *Writer) Close() error {
	w.mu.Lock()
	if w.isClosed {
		w.mu.Unlock()

		return nil
	}

	w.isClosed = true
	w.mu.Unlock()

	close(w.stop)
	<-w.stopped

	w.syncOnce()

	closeErr := w.file.Close()

	w.mu.Lock()
	defer w.mu.Unlock()

	return errors.Join(w.err, closeErr)
}

func (w *Writer) appendLocked(record Record) (Record, error) {
	if w.err != nil {
		return Record{}, w.err
	}

	if w.isClosed {
		return Record{}, ErrWriterClosed
	}

	now := time.Now()
	record.Index = w.nextIndex
	record.Round = w.round
	record.TimeNS = now.Sub(w.started).Nanoseconds()
	record.Wall = now.UTC().Format(wallFormat)

	line, err := json.Marshal(record)
	if err != nil {
		return Record{}, fmt.Errorf("encoding history record: %w", err)
	}

	_, err = w.buffer.Write(append(line, '\n'))
	if err != nil {
		w.fail(err)

		return Record{}, w.err
	}

	w.nextIndex++
	w.lastWritten = record.Index

	return record, nil
}

func (w *Writer) syncLoop() {
	defer close(w.stopped)

	ticker := time.NewTicker(groupSyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			w.syncOnce()
		}
	}
}

// syncOnce flushes the buffer under the lock and fsyncs outside it, so
// appends keep flowing while the disk works.
func (w *Writer) syncOnce() {
	w.mu.Lock()

	hasPending := w.lastWritten > w.lastSynced && w.err == nil
	if !hasPending {
		w.mu.Unlock()

		return
	}

	err := w.buffer.Flush()
	target := w.lastWritten
	w.mu.Unlock()

	if err == nil {
		err = w.file.Sync()
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if err != nil {
		w.fail(err)

		return
	}

	w.lastSynced = target
	w.synced.Broadcast()
}

// fail records the first write error and wakes every waiter; the history is
// unusable from here on.
func (w *Writer) fail(err error) {
	if w.err == nil {
		w.err = fmt.Errorf("writing history: %w", err)
	}

	w.synced.Broadcast()
}
