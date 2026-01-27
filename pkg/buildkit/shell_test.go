//nolint:testpackage
package buildkit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/radiofrance/dib/pkg/mock"
	"github.com/radiofrance/dib/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//nolint:tparallel
func TestShellBuilder_Build(t *testing.T) {
	// Here, we need to mock a buildctl binary in the $PATH, because the ShellBuilder uses exec.LookPath to find it.
	// So we create a temporary directory with a fake buildctl binary and prepend that directory to the $PATH.
	tempDir := t.TempDir()
	fakeBuildctlPath := filepath.Join(tempDir, "buildctl")
	require.NoError(t, os.WriteFile(fakeBuildctlPath, []byte("#!/bin/sh\necho fake buildctl"), 0o755)) //nolint:gosec
	t.Setenv("PATH", fmt.Sprintf("%s:%s", tempDir, os.Getenv("PATH")))

	testCases := []struct {
		name                  string
		modifyOpts            func(opts *types.ImageBuilderOpts)
		expectedBuildArgsFunc func(context string) []string
		expectedError         error
	}{
		{
			name: "Executes",
			modifyOpts: func(opts *types.ImageBuilderOpts) {
				opts.LocalOnly = true
			},
			expectedBuildArgsFunc: func(context string) []string {
				return []string{
					fmt.Sprintf("--addr=%s", getBuildkitHostAddress()),
					"build",
					"--progress=auto",
					"--frontend=dockerfile.v0",
					fmt.Sprintf("--local=context=%s", context),
					"--output=type=image,unpack=true,name=gcr.io/project-id/image:version,name=gcr.io/project-id/image:latest,push=true", //nolint:lll
					fmt.Sprintf("--local=dockerfile=%s", context),
					"--opt=filename=Dockerfile",
					"--opt=build-arg:someArg=someValue",
					"--opt=label:someLabel=someValue",
				}
			},
			expectedError: nil,
		},
		{
			name: "ExecutesDisablePush",
			modifyOpts: func(opts *types.ImageBuilderOpts) {
				opts.Push = false
				opts.LocalOnly = true
			},
			expectedBuildArgsFunc: func(context string) []string {
				return []string{
					fmt.Sprintf("--addr=%s", getBuildkitHostAddress()),
					"build",
					"--progress=auto",
					"--frontend=dockerfile.v0",
					fmt.Sprintf("--local=context=%s", context),
					"--output=type=image,unpack=true,name=gcr.io/project-id/image:version,name=gcr.io/project-id/image:latest",
					fmt.Sprintf("--local=dockerfile=%s", context),
					"--opt=filename=Dockerfile",
					"--opt=build-arg:someArg=someValue",
					"--opt=label:someLabel=someValue",
				}
			},
			expectedError: nil,
		},
		{
			name: "ExecutesWithoutTags",
			modifyOpts: func(opts *types.ImageBuilderOpts) {
				opts.Tags = nil
				opts.LocalOnly = true
			},
			expectedBuildArgsFunc: func(context string) []string {
				return []string{
					fmt.Sprintf("--addr=%s", getBuildkitHostAddress()),
					"build",
					"--progress=auto",
					"--frontend=dockerfile.v0",
					fmt.Sprintf("--local=context=%s", context),
					"--output=type=image,unpack=true,dangling-name-prefix=<none>,push=true",
					fmt.Sprintf("--local=dockerfile=%s", context),
					"--opt=filename=Dockerfile",
					"--opt=build-arg:someArg=someValue",
					"--opt=label:someLabel=someValue",
				}
			},
			expectedError: nil,
		},
		{
			name: "ExecutesWithFile",
			modifyOpts: func(opts *types.ImageBuilderOpts) {
				opts.File = filepath.Join(opts.Context, defaultDockerfileName)
				opts.LocalOnly = true
			},
			expectedBuildArgsFunc: func(context string) []string {
				return []string{
					fmt.Sprintf("--addr=%s", getBuildkitHostAddress()),
					"build",
					"--progress=auto",
					"--frontend=dockerfile.v0",
					fmt.Sprintf("--local=context=%s", context),
					"--output=type=image,unpack=true,name=gcr.io/project-id/image:version,name=gcr.io/project-id/image:latest,push=true", //nolint:lll
					fmt.Sprintf("--local=dockerfile=%s", context),
					"--opt=filename=Dockerfile",
					"--opt=build-arg:someArg=someValue",
					"--opt=label:someLabel=someValue",
				}
			},
			expectedError: nil,
		},
		{
			name: "ExecutesWithTarget",
			modifyOpts: func(opts *types.ImageBuilderOpts) {
				opts.Target = "prod"
				opts.LocalOnly = true
			},
			expectedBuildArgsFunc: func(context string) []string {
				return []string{
					fmt.Sprintf("--addr=%s", getBuildkitHostAddress()),
					"build",
					"--progress=auto",
					"--frontend=dockerfile.v0",
					fmt.Sprintf("--local=context=%s", context),
					"--output=type=image,unpack=true,name=gcr.io/project-id/image:version,name=gcr.io/project-id/image:latest,push=true", //nolint:lll
					fmt.Sprintf("--local=dockerfile=%s", context),
					"--opt=filename=Dockerfile",
					"--opt=build-arg:someArg=someValue",
					"--opt=label:someLabel=someValue",
					fmt.Sprintf("--opt=target=%s", "prod"),
				}
			},
			expectedError: nil,
		},
		{
			name:                  "FailsOnExecutorError",
			modifyOpts:            func(opts *types.ImageBuilderOpts) {},
			expectedBuildArgsFunc: nil,
			expectedError:         errors.New("something wrong happened"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fakeExecutor := mock.NewShellExecutor(nil)
			if tc.name == "FailsOnExecutorError" {
				fakeExecutor = mock.NewShellExecutor([]mock.ExecutorResult{
					{
						Output: "",
						Error:  errors.New("something wrong happened"),
					},
				})
			}

			opts := provideDefaultOptions(t)
			tc.modifyOpts(&opts)

			b := ShellBuilder{
				executor:        fakeExecutor,
				contextProvider: &mockContextProvider{opts.Context},
			}

			err := b.Build(context.Background(), opts)
			if tc.expectedError != nil {
				require.EqualError(t, err, tc.expectedError.Error())
			} else {
				require.NoError(t, err)
				assert.Len(t, fakeExecutor.Executed, 1)
				assert.Equal(t, fakeBuildctlPath, fakeExecutor.Executed[0].Command)

				expectedBuildArgs := tc.expectedBuildArgsFunc(opts.Context)
				assert.ElementsMatch(t, expectedBuildArgs, fakeExecutor.Executed[0].Args)
			}
		})
	}
}
