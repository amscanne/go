package main

import (
	"flag"
	"regexp"
	"sort"
	"strconv"

        "cmd/internal/objfile"
)

var (
	branchMinimum       = flag.Int("branch_minimum", 100, "minumum branches to create a recommendation")
	branchLowThreshold  = flag.Float64("branch_low_threshold", 0.10, "success threshold below which recommendations are made")
	branchHighThreshold = flag.Float64("branch_high_threshold", 0.90, "success threshold for layout optimizations")
)

// branchKey represents a single edge for a branch.
type branchKey struct {
	source uint64
	target uint64
}

// branchStats are stats for a single source->target branch.
type branchStats struct {
	predicted int
	missed    int
}

// brstackRegex matches the format produces by perf script.
var brstackRegex = regexp.MustCompile("(0x[0-9a-f]+)/(0x[0-9a-f]+)/(M|P|-)/(X|-)/(A|-)/([0-9]+)")

// extractBranches extracts all branch data.
func extractBranches(exe *objfile.File) error {
	liner, err := exe.PCLineTable()
	if err != nil {
		return err
	}
	disasm, err := exe.Disasm()
	if err != nil {
		return err
	}

	// Extract all perf data with respect to branches.
	bi := make(map[branchKey]*branchStats)
	if err := runPerfScript(brstackRegex, "brstack", func(parts []string) {
		// Extract the source and target.
		source, err := strconv.ParseUint(parts[0][2:], 16, 64)
		if err != nil {
			return
		}
		target, err := strconv.ParseUint(parts[1][2:], 16, 64)
		if err != nil {
			return
		}
		if source > userTop || target > userTop {
			return // Skip kernel addresses.
		}
		key := branchKey{
			source: source,
			target: target,
		}

		// Update the stats.
		stats, ok := bi[key]
		if !ok {
			stats = new(branchStats)
			bi[key] = stats
		}
		if parts[2] == "P" {
			stats.predicted++
		} else if parts[2] == "M" {
			stats.missed++
		}
	}); err != nil {
		return err
	}

	// Extract aggregrate information.
	totals := make(map[branchKey]int)
	hits := make(map[branchKey]float64)
	sortedByTotal := make([]branchKey, 0, len(bi))
	sortedByHit := make([]branchKey, 0, len(bi))
	for key, stats := range bi {
		total := stats.predicted + stats.missed
		if total < *branchMinimum {
			continue // Not interesting.
		}
		hit := float64(stats.predicted)/float64(total)
		if hit > *branchLowThreshold && hit < *branchHighThreshold {
			continue // Not interesting.
		}
		totals[key] = total
		hits[key] = float64(stats.predicted)/float64(total)
		sortedByTotal = append(sortedByTotal, key)
		sortedByHit = append(sortedByHit, key)
	}
	sort.Slice(sortedByTotal, func(i, j int) bool { return totals[sortedByTotal[i]] > totals[sortedByTotal[j]] })
	sort.Slice(sortedByHit, func(i, j int) bool { return hits[sortedByHit[i]] < hits[sortedByHit[j]] })

	// Do we produce anything at all?
	if len(sortedByTotal) == 0 {
		return nil
	}

	// Dump out top results.
	printBranch := func(key branchKey) {
		debug("  0x%x->0x%x: %d (%2.2f)", key.source, key.target, totals[key], hits[key])
	}
	debug("Most hit branches:")
	for _, key := range sortedByTotal {
		printBranch(key)
	}
	debug("Worst & best predicted branches:")
	for _, key := range sortedByHit {
		printBranch(key)
	}

	// If the source for this file indicates that the correctly predicted
	// branch target is a jump instruction, then we can improve performance
	// by optimizing in the appropriate directly. This tests if the given
	// branch is a jump by seeing if the target is the next contiguous
	// instruction.
	isJump := func(key branchKey) (jump bool) {
		// FIXME: as a heuristic, we just check if the target follows
		// within 16 bytes of the source. In the future, we should use
		// the objfile disassembly itself for this check.
		_ = disasm
		return key.target > key.source && key.target <= key.source+16
	}

	candidates := make(map[branchKey]bool)
	for _, key := range sortedByTotal {
		if hits[key] < *branchLowThreshold && isJump(key) {
			// We're not taking the jump when we should be. This
			// suggests that we should lay out the code as in the
			// "likely" case.
			candidates[key] = true
		} else if hits[key] < *branchLowThreshold && !isJump(key) {
			// We are taking the jump when we should be? This is
			// strange, as without a prediction the CPU should
			// continue execution in a straight line. Presumably
			// this might be some kind of clash on the predictor
			// lines, so we shouldn't recommend anything.
		} else if hits[key] > *branchHighThreshold && isJump(key) {
			// The predictor is getting it right here, but it is
			// laid out as a jump. This has the effect of producing
			// slower code than a contiguous code block. We can
			// mark this as likely as well.
			candidates[key] = true
		}
	}

	// Dump out all candidates.
	if len(candidates) > 0 {
		debug("Optimization candidates:")
	}
	for key := range candidates {
		filename, line, fn := liner.PCToLine(key.source)
		printBranch(key) // Show raw information.
		if fn != nil && filename != "" && line > 0 {
			debug("  %s:%d (%2.2f)", filename, line, hits[key])
		}
	}

	return nil
}

// applyBranches applies branch feedback data.
func applyBranches(exe *objfile.File) error {
	if err := extractBranches(exe); err != nil {
		return err
	}

	return nil // All done.
}
