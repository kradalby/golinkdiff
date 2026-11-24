// Copyright (c) 2026 Kristoffer Dalby
// SPDX-License-Identifier: BSD-3-Clause

// Command golinkdiff shows what moved in go links since it last ran.
package main

import (
	"os"

	"github.com/kradalby/golinkdiff/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
