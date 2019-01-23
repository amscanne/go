// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"syscall"

	"cmd/internal/objfile"
)

const usageMessage = "" +
	`Usage of 'go tool fdo':

Record a profile using the perf tool:
	go tool fdo -record -perfdata=data <command-line...>

A profile can also be recorded manually with the perf tool.

Apply branch intrinsics:
	go tool fdo -apply -perfdata=data -binary=bin -branches
`

func usage() {
	fmt.Fprintln(os.Stderr, usageMessage)
	fmt.Fprintln(os.Stderr, "Flags:")
	flag.PrintDefaults()
	fmt.Fprintln(os.Stderr, "\n  Exactly one of -record, or -apply must be set.")
	os.Exit(2)
}

func debug(format string, args... interface{}) {
	log.Printf(format, args...)
}

var (
	record   = flag.Bool("record", false, "record performance data")
	apply    = flag.Bool("apply", false, "apply feedback-directed optimizations")
	perfdata = flag.String("perfdata", "perf.data", "performance data file for input/output")
	binary   = flag.String("binary", "", "binary for symbolization")
	branches = flag.Bool("branches", false, "apply branch intrinsics")
)

const (
	userTop = 0x00007fffffffffff
)

func main() {
	flag.Usage = usage
	flag.Parse()

	// Usage information when no arguments.
	if flag.NFlag() == 0 && flag.NArg() == 0 {
		flag.Usage()
	}

	// At least one mode is required.
	if (!*record && !*apply) || (*record && *apply) {
		fmt.Fprintf(os.Stderr, `Exactly one of -record, or -apply must be set.`)
		fmt.Fprintln(os.Stderr, `For usage information, run "go tool fdo -help"`)
		os.Exit(2)
	} else if *record && flag.NArg() == 0 {
		flag.Usage()
	} else if *apply && flag.NArg() != 0 {
		flag.Usage()
	}

	// Run the appropriate mode.
	if *record {
		if err := recordPerf(flag.Args()); err != nil {
			fmt.Fprintf(os.Stderr, "fdo record: %v\n", err)
			os.Exit(2)
		}
	} else {
		if err := applyPerf(); err != nil {
			fmt.Fprintf(os.Stderr, "fdo apply: %v\n", err)
			os.Exit(2)
		}
	}
}

// recordPerf records a perf profile for the given command.
//
// Effectively, this just runs the perf program with appropriate arguments.
// This can be done manually, and is simply a convenience.
//
// Note that this function should not return.
func recordPerf(command []string) error {
	perf, err := exec.LookPath("perf")
	if err != nil {
		return err
	}
	return syscall.Exec(perf, append([]string{
		"perf", "record",
		"-b", // Branch data.
		"--", // Stop processing arguments.
	}, command...), os.Environ())
}

// runPerfScript runs perf script on the profile data, extracts anything
// matching the given regular expression, and calls the provided function for
// all matches.
func runPerfScript(matcher *regexp.Regexp, field string, fn func([]string)) error {
	cmd := exec.Command(
		"perf", "script",
		"-F", field,
		"-i", *perfdata,
	)
	cmd.Stderr = os.Stderr // Pass through error output.

	// Extract stdout to process.
	r, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	defer r.Close()

	// Start the process.
	if err := cmd.Start(); err != nil {
		return err
	}

	// Read and process all data. We copy the data 1k at a time into a
	// temporary buffer, which we scan for matches exhaustively. If any
	// matches are found, everything up until the end of the last match is
	// moved to the front of the buffer and the work continues.
	bufR := bufio.NewReader(r)
	tmpB := make([]byte, 0, 1024)
	for {
		// Are there are matches in the buffer?
		if is := matcher.FindSubmatchIndex(tmpB); is != nil {
			ss := make([]string, 0, len(is)/2)
			for i := 2; i < len(is); i += 2 {
				ss = append(ss, string(tmpB[is[i]:is[i+1]]))
			}
			fn(ss)                   // Call with the match slice.
			l := len(tmpB) - is[1]   // Length of remaining text.
			copy(tmpB, tmpB[is[1]:]) // Copy non-match to beginning.
			tmpB = tmpB[:l]          // Truncate.
			continue                 // Find next match.
		}

		// Extend the existing slice.
		l := len(tmpB)
		tmpB = append(tmpB, make([]byte, 1024)...)
		n, err := bufR.Read(tmpB[l:])
		if err != nil && err != io.EOF {
			return err
		}
		tmpB = tmpB[:l+n] // Recut.
		if n == 0 && err == io.EOF {
			break // Finished.
		}
	}

	// Wait for the program to exit.
	if err := cmd.Wait(); err != nil {
		return err
	}

	return nil
}

// applyPerf applies the given perf profile in the current directory.
//
// This uses the perf tool to extract relevant data from the perf data, and
// applies relevant intrinsics to the AST.
func applyPerf() error {
	// Open the main binary.
	exe, err := objfile.Open(*binary)
	if err != nil {
		return err
	}

	// Start applying optimizations.
	if *branches {
		if err := applyBranches(exe); err != nil {
			return err
		}
	}

	return nil
}
