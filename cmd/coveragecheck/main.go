// coveragecheck enforces statement coverage from a Go cover profile.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
)

func coverage(r io.Reader) (covered, total uint64, err error) {
	s := bufio.NewScanner(r)
	if !s.Scan() || (s.Text() != "mode: set" && s.Text() != "mode: count" && s.Text() != "mode: atomic") {
		return 0, 0, fmt.Errorf("missing or invalid coverage mode")
	}
	type block struct {
		statements uint64
		covered    bool
	}
	seen := map[string]block{}
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) != 3 {
			return 0, 0, fmt.Errorf("invalid coverage record %q", s.Text())
		}
		n, e := strconv.ParseUint(fields[1], 10, 64)
		if e != nil {
			return 0, 0, e
		}
		count, e := strconv.ParseUint(fields[2], 10, 64)
		if e != nil {
			return 0, 0, e
		}
		// -coverpkg=./... emits the same source block from several test
		// binaries. Count each statement once and union actual execution.
		if previous, exists := seen[fields[0]]; exists {
			if previous.statements != n {
				return 0, 0, fmt.Errorf("inconsistent coverage block %q", fields[0])
			}
			if !previous.covered && count > 0 {
				covered += n
				previous.covered = true
				seen[fields[0]] = previous
			}
			continue
		}
		seen[fields[0]] = block{statements: n, covered: count > 0}
		if n > math.MaxUint64-total {
			return 0, 0, fmt.Errorf("statement count overflow")
		}
		total += n
		if count > 0 {
			covered += n
		}
	}
	if err := s.Err(); err != nil {
		return 0, 0, err
	}
	if total == 0 {
		return 0, 0, fmt.Errorf("empty coverage profile")
	}
	return covered, total, nil
}

func run(args []string, out io.Writer) error {
	f := flag.NewFlagSet("coveragecheck", flag.ContinueOnError)
	f.SetOutput(out)
	profile := f.String("profile", "coverage/backend.out", "Go coverage profile")
	minimum := f.Float64("minimum", 95, "minimum statement coverage percentage")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || math.IsNaN(*minimum) || math.IsInf(*minimum, 0) || *minimum < 0 || *minimum > 100 {
		return fmt.Errorf("minimum must be between 0 and 100; unexpected arguments are not allowed")
	}
	file, err := os.Open(*profile)
	if err != nil {
		return err
	}
	defer file.Close()
	covered, total, err := coverage(file)
	if err != nil {
		return err
	}
	percentage := 100 * float64(covered) / float64(total)
	fmt.Fprintf(out, "Backend statements: %d/%d (%.4f%%); required %.2f%%\n", covered, total, percentage, *minimum)
	if percentage < *minimum {
		return fmt.Errorf("backend coverage is below the required threshold")
	}
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
