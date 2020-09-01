// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
FDO is a program for recording perf profiles, extracting performance data, and
applying feedback-directed optimization.

First, a profile must be generated for the binary. This can be done via `go
tool fdo -record ...` or manually with the perf tool. The tool will use binary
debug information and store only feedback to inform optimizations.

Next, the optimizations can be applied via `go tool fdo -apply`.

For usage information, please see:
	go tool fdo -help
*/
package main
