//go:build windows

package main

import (
	"context"

	"github.com/ClaymanTwinkle/lark-agent-bot/config"
)

func runRunAsUserStartupChecks(_ context.Context, _ *config.Config) error {
	return nil
}
