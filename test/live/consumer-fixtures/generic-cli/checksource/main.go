// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

// Command checksource runs the generic Go CLI adapter's pristine-source gate.
// It is acceptance-fixture tooling, not a distributed Forge CI command.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := genericCLIChecks(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "generic CLI source checks: %v\n", err)
		os.Exit(1)
	}
}
