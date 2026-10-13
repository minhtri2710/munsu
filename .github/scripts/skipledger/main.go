// Command skipledger compares the tests the CI lanes skipped with
// .github/skips.allow, in both directions.
//
//	skipledger check <allow> <lane.json>...   print unlisted skips and stale rows
//	skipledger list <lane.json>...            print observed skips as allow rows
//
// Each lane file is `go test -json` output; the lane name is its file name
// without .json. A skip is a test-level event with action "skip". The reason is
// the last `file_test.go:N: message` line the test printed before it skipped.
//
// check is report-only: findings print and the exit status stays 0. An input
// that cannot be read is the one failure, because the lane would then have
// observed nothing and a clean report would be false. Rows match on package and
// test; the reason column is documentation and is not compared.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type key struct{ pkg, test string }

type skip struct {
	key
	reason string
	lanes  map[string]bool
}

type row struct {
	key
	reason string
}

type event struct {
	Action  string
	Package string
	Test    string
	Output  string
}

var reasonLine = regexp.MustCompile(`^[^\s:]+_test\.go:\d+: (.+)$`)

const usage = "usage: skipledger check <allow> <lane.json>... | skipledger list <lane.json>..."

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "::error::skipledger: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) < 2 {
		return errors.New(usage)
	}
	switch args[0] {
	case "list":
		skips, err := readLanes(args[1:])
		if err != nil {
			return err
		}
		for _, s := range sortSkips(skips) {
			fmt.Fprintf(out, "%s %s %s\n", s.pkg, s.test, s.reason)
		}
		return nil
	case "check":
		if len(args) < 3 {
			return errors.New(usage)
		}
		rows, err := readAllow(args[1])
		if err != nil {
			return err
		}
		skips, err := readLanes(args[2:])
		if err != nil {
			return err
		}
		report(out, args[1], rows, skips)
		return nil
	}
	return errors.New(usage)
}

func readLanes(paths []string) (map[key]*skip, error) {
	skips := map[key]*skip{}
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		lane := strings.TrimSuffix(filepath.Base(path), ".json")
		err = readSkips(f, lane, skips)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return skips, nil
}

func readSkips(r io.Reader, lane string, skips map[key]*skip) error {
	dec := json.NewDecoder(r)
	lastReason := map[key]string{}
	for {
		var ev event
		err := dec.Decode(&ev)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if ev.Test == "" {
			continue
		}
		k := key{ev.Package, ev.Test}
		switch ev.Action {
		case "output":
			if m := reasonLine.FindStringSubmatch(strings.TrimSpace(ev.Output)); m != nil {
				lastReason[k] = m[1]
			}
		case "skip":
			s := skips[k]
			if s == nil {
				reason := lastReason[k]
				if reason == "" {
					reason = "(no message)"
				}
				s = &skip{key: k, reason: reason, lanes: map[string]bool{}}
				skips[k] = s
			}
			s.lanes[lane] = true
		}
	}
}

func readAllow(path string) (map[key]row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows := map[key]row{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return nil, fmt.Errorf("%s:%d: want `package test reason`", path, n)
		}
		rest := strings.TrimSpace(line[len(fields[0]):])
		rest = strings.TrimSpace(rest[len(fields[1]):])
		k := key{fields[0], fields[1]}
		if _, dup := rows[k]; dup {
			return nil, fmt.Errorf("%s:%d: duplicate row for %s %s", path, n, k.pkg, k.test)
		}
		rows[k] = row{key: k, reason: rest}
	}
	return rows, sc.Err()
}

func report(out io.Writer, allow string, rows map[key]row, skips map[key]*skip) {
	name := filepath.Base(allow)
	var unlisted []*skip
	for k, s := range skips {
		if _, ok := rows[k]; !ok {
			unlisted = append(unlisted, s)
		}
	}
	var stale []row
	for k, r := range rows {
		if _, ok := skips[k]; !ok {
			stale = append(stale, r)
		}
	}
	sort.Slice(stale, func(i, j int) bool { return lessKey(stale[i].key, stale[j].key) })
	sort.Slice(unlisted, func(i, j int) bool { return lessKey(unlisted[i].key, unlisted[j].key) })

	fmt.Fprintf(out, "skipledger: %d skipped test(s), %d row(s) in %s\n", len(skips), len(rows), name)
	if len(unlisted) == 0 && len(stale) == 0 {
		fmt.Fprintln(out, "skipledger: every skip is listed and every row is skipped")
		return
	}
	fmt.Fprintf(out, "unlisted skips (no row in %s): %d\n", name, len(unlisted))
	for _, s := range unlisted {
		fmt.Fprintf(out, "  %s %s [%s] %s\n", s.pkg, s.test, laneList(s.lanes), s.reason)
	}
	fmt.Fprintf(out, "stale rows (skipped by no lane): %d\n", len(stale))
	for _, r := range stale {
		fmt.Fprintf(out, "  %s %s %s\n", r.pkg, r.test, r.reason)
	}
}

func sortSkips(skips map[key]*skip) []*skip {
	list := make([]*skip, 0, len(skips))
	for _, s := range skips {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool { return lessKey(list[i].key, list[j].key) })
	return list
}

func lessKey(a, b key) bool {
	if a.pkg != b.pkg {
		return a.pkg < b.pkg
	}
	return a.test < b.test
}

func laneList(lanes map[string]bool) string {
	names := make([]string, 0, len(lanes))
	for name := range lanes {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}
