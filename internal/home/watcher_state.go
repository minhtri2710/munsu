package home

import (
	"bufio"
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
	_ = os.WriteFile(p, []byte(fmt.Sprintf("%d %d", time.Now().Unix(), os.Getpid())), 0644)
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

var wakeSeq int64

func EnqueueWake(h, kind, key, payload string) error {
	p := WakeQueuePath(h)
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		return e
	}
	f, e := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if e != nil {
		return e
	}
	defer f.Close()
	_, e = fmt.Fprintf(f, "%d\t%d-%d\t%s\t%s\t%s\n", time.Now().Unix(), os.Getpid(), atomic.AddInt64(&wakeSeq, 1), kind, key, payload)
	return e
}
func DrainWakes(h string) ([]WakeRecord, error) {
	p := WakeQueuePath(h)
	f, e := os.Open(p)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var out []WakeRecord
	s := bufio.NewScanner(f)
	for s.Scan() {
		x := strings.SplitN(s.Text(), "\t", 5)
		if len(x) == 5 {
			out = append(out, WakeRecord{Epoch: x[0], Seq: x[1], Kind: x[2], Key: x[3], Payload: x[4]})
		}
	}
	if e = s.Err(); e != nil {
		f.Close()
		return nil, e
	}
	_ = f.Close()
	if e = os.Remove(p); e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	return out, nil
}
func HasQueuedWakes(h string) bool {
	i, e := os.Stat(WakeQueuePath(h))
	return e == nil && i.Size() > 0
}
