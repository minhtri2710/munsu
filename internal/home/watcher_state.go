package home

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

const watcherStaleThreshold = 300 * time.Second
const wakeQueueFile = "state/.wake-queue"
const watcherBeatFile = "state/.last-watcher-beat"

type WakeRecord struct{ Epoch, Seq, Kind, Key, Payload string }
type WatcherBeatStatus struct {
	Exists, Stale bool
	Age           time.Duration
}

func WatcherStaleThreshold() time.Duration { return watcherStaleThreshold }
func WatcherBeatPath(h string) string      { return filepath.Join(h, watcherBeatFile) }
func WakeQueuePath(h string) string        { return filepath.Join(h, wakeQueueFile) }
func WriteWatcherBeat(h string) {
	p := WatcherBeatPath(h)
	_ = os.MkdirAll(filepath.Dir(p), 0755)
	_ = canonicalAtomicWrite(p, []byte(fmt.Sprintf("%d %d", time.Now().Unix(), os.Getpid())))
}
func ReadWatcherBeat(h string) (ts int64, pid int, ok bool) {
	b, e := os.ReadFile(WatcherBeatPath(h))
	if e != nil {
		return
	}
	_, e = fmt.Sscanf(strings.TrimSpace(string(b)), "%d %d", &ts, &pid)
	if e != nil {
		_, e = fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &ts)
	}
	return ts, pid, e == nil
}
func ClearWatcherBeat(h string) { _ = os.Remove(WatcherBeatPath(h)) }
func ReadWatcherBeatStatus(h string, now time.Time) WatcherBeatStatus {
	ts, _, ok := ReadWatcherBeat(h)
	if !ok {
		return WatcherBeatStatus{Stale: true}
	}
	age := now.Sub(time.Unix(ts, 0))
	return WatcherBeatStatus{Exists: true, Stale: age > watcherStaleThreshold, Age: age}
}

func EnqueueWake(h, kind, key, payload string) error {
	return enqueueWakeAt(h, kind, key, payload, time.Now())
}

func enqueueWakeAt(h, kind, key, payload string, at time.Time) (err error) {
	lock, err := acquireWakeLock(h)
	if err != nil {
		return err
	}
	defer joinWakeLockError(&err, lock)
	if err := recoverWakeMutationLocked(h); err != nil {
		return err
	}
	return enqueueWakeLocked(h, kind, key, payload, at)
}

func enqueueWakeLocked(h, kind, key, payload string, at time.Time) error {
	records, err := readWakeQueue(h)
	if err != nil {
		return err
	}
	records = append(records, newWakeRecord(kind, key, payload, at))
	return applyWakeMutationLocked(h, wakeMutation{
		queueSet:  true,
		queueData: wakeQueueData(records),
	})
}

// DrainWakes reads every wake record out of the queue and removes the queue
// file. The queue read and removal are one locked wake mutation, so a producer
// cannot append between the read and removal.
func DrainWakes(h string) (records []WakeRecord, err error) {
	lock, err := acquireWakeLock(h)
	if err != nil {
		return nil, err
	}
	defer joinWakeLockError(&err, lock)
	if err := recoverWakeMutationLocked(h); err != nil {
		return nil, err
	}
	records, err = readWakeQueue(h)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(WakeQueuePath(h)); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if err := applyWakeMutationLocked(h, wakeMutation{queueSet: true}); err != nil {
		return nil, err
	}
	return records, nil
}

func newWakeRecord(kind, key, payload string, at time.Time) WakeRecord {
	return WakeRecord{
		Epoch:   fmt.Sprint(at.Unix()),
		Seq:     fmt.Sprintf("%d-%d", os.Getpid(), atomic.AddInt64(&wakeSeq, 1)),
		Kind:    kind,
		Key:     key,
		Payload: payload,
	}
}

func HasQueuedWakes(h string) bool {
	journal, err := readWakeMutationJournal(h)
	if err != nil {
		return true
	}
	if journal != nil && journal.State == "pending" {
		return true
	}
	i, err := os.Stat(WakeQueuePath(h))
	if os.IsNotExist(err) {
		return false
	}
	if err != nil || i.IsDir() {
		return true
	}
	return i.Size() > 0
}
