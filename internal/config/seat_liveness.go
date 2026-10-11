package config

import (
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"time"
)

const (
	keySeatSilentAfter = "seat-silent-after"
	keySeatNudge       = "seat-repeat-nudge"
	keySeatEnd         = "seat-repeat-end"

	defaultSeatSilentAfter = 5 * time.Minute
	defaultSeatNudge       = 5
	defaultSeatEnd         = 8
)

// SeatLiveness is the watcher's seat liveness thresholds: a seat silent for
// SilentAfter is one incident, and a seat repeating the same failing status
// line is one incident at RepeatNudge and another at RepeatEnd.
type SeatLiveness struct {
	SilentAfter time.Duration
	RepeatNudge int
	RepeatEnd   int
}

// ReadSeatLiveness returns the thresholds from config. An unset key takes its
// starting value; a set key that is invalid is an error naming the key and its
// valid range.
func ReadSeatLiveness(homeDir string) (SeatLiveness, error) {
	lv := SeatLiveness{SilentAfter: defaultSeatSilentAfter, RepeatNudge: defaultSeatNudge, RepeatEnd: defaultSeatEnd}
	if v, ok, err := getSet(homeDir, keySeatSilentAfter); err != nil {
		return lv, err
	} else if ok {
		if lv.SilentAfter, err = parseSilentAfter(v); err != nil {
			return lv, err
		}
	}
	if v, ok, err := getSet(homeDir, keySeatNudge); err != nil {
		return lv, err
	} else if ok {
		if lv.RepeatNudge, err = parseRepeat(keySeatNudge, v); err != nil {
			return lv, err
		}
	}
	if v, ok, err := getSet(homeDir, keySeatEnd); err != nil {
		return lv, err
	} else if ok {
		if lv.RepeatEnd, err = parseRepeat(keySeatEnd, v); err != nil {
			return lv, err
		}
	}
	return lv, lv.checkOrder()
}

// validateSeatLivenessSet checks one value before Set stores it. The order of
// nudge and end is checked against the other key only when that key holds a
// valid value; an invalid one is reported by ReadSeatLiveness, so a bad file
// can always be repaired one key at a time.
func validateSeatLivenessSet(homeDir, key, value string) error {
	if key == keySeatSilentAfter {
		_, err := parseSilentAfter(value)
		return err
	}
	n, err := parseRepeat(key, value)
	if err != nil {
		return err
	}
	lv := SeatLiveness{RepeatNudge: defaultSeatNudge, RepeatEnd: defaultSeatEnd}
	other, otherSet := keySeatEnd, &lv.RepeatEnd
	this := &lv.RepeatNudge
	if key == keySeatEnd {
		other, otherSet = keySeatNudge, &lv.RepeatNudge
		this = &lv.RepeatEnd
	}
	*this = n
	if v, ok, gerr := getSet(homeDir, other); gerr == nil && ok {
		m, perr := parseRepeat(other, v)
		if perr != nil {
			return nil
		}
		*otherSet = m
	}
	return lv.checkOrder()
}

func isSeatLivenessKey(key string) bool {
	return key == keySeatSilentAfter || key == keySeatNudge || key == keySeatEnd
}

func getSet(homeDir, key string) (string, bool, error) {
	v, err := Get(homeDir, key)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	return v, err == nil, err
}

func parseSilentAfter(v string) (time.Duration, error) {
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("config %s %q: want a positive duration such as 5m", keySeatSilentAfter, v)
	}
	return d, nil
}

func parseRepeat(key, v string) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil || n < 2 {
		return 0, fmt.Errorf("config %s %q: want an integer of at least 2", key, v)
	}
	return n, nil
}

func (lv SeatLiveness) checkOrder() error {
	if lv.RepeatEnd <= lv.RepeatNudge {
		return fmt.Errorf("config %s %d: want an integer greater than %s %d", keySeatEnd, lv.RepeatEnd, keySeatNudge, lv.RepeatNudge)
	}
	return nil
}
