//go:build headless || (!windows && !cgo)

package tray

import (
	"context"

	"github.com/rscprinting/print-agent/internal/config"
	"github.com/rscprinting/print-agent/internal/wsclient"
)

type Tray struct{}

func New(cfg config.Config, c *wsclient.Client, onRestart, onUpdate, onShutdown func()) *Tray {
	return &Tray{}
}

func (t *Tray) Run(ctx context.Context) {
	<-ctx.Done()
}
