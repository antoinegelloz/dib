package buildkit

import (
	"context"
	"fmt"

	"github.com/radiofrance/dib/pkg/buildcontext"
	"github.com/radiofrance/dib/pkg/executor"
	"github.com/radiofrance/dib/pkg/types"
)

type ShellBuilder struct {
	contextProvider buildcontext.ContextProvider
	executor        executor.ShellExecutor
}

// NewShellBuilder creates a new instance of K8sBuilder to use the shell executor to build images.
func NewShellBuilder(shell executor.ShellExecutor) *ShellBuilder {
	return &ShellBuilder{
		executor:        shell,
		contextProvider: buildcontext.NewLocalContextProvider(),
	}
}

// Build the image using the shell executor.
func (b *ShellBuilder) Build(ctx context.Context, opts types.ImageBuilderOpts) error {
	var err error

	opts.Context, err = b.contextProvider.PrepareContext(ctx, opts)
	if err != nil {
		return fmt.Errorf("cannot prepare buildkit build context: %w", err)
	}

	buildctlArgs, err := generateBuildctlArgs(opts)
	if err != nil {
		return err
	}

	buildctlBinary, err := BuildctlBinary()
	if err != nil {
		return fmt.Errorf("cannot find buildctl binary: %w", err)
	}

	return b.executor.ExecuteStdout(buildctlBinary, buildctlArgs...)
}
