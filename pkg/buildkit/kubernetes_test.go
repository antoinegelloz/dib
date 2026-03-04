//nolint:testpackage
package buildkit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	k8sutils "github.com/radiofrance/dib/pkg/kubernetes"
	"github.com/radiofrance/dib/pkg/mock"
	"github.com/radiofrance/dib/pkg/testutil"
	"github.com/radiofrance/dib/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestNewK8sBuilder(t *testing.T) {
	t.Parallel()

	t.Run("empty config", func(t *testing.T) {
		t.Parallel()

		b, err := NewK8sBuilder(context.Background(), Config{})
		require.Nil(t, b)
		require.ErrorContains(t, err, "either Azure or S3 must be configured for build context upload")
	})

	t.Run("both Azure and S3", func(t *testing.T) {
		t.Parallel()

		b, err := NewK8sBuilder(context.Background(), Config{
			Context: Context{
				Azure: Azure{
					AccountName: "account_name",
					Container:   "container",
				},
				S3: S3{
					Bucket: "bucket",
					Region: "region",
				},
			},
		})
		require.Nil(t, b)
		require.ErrorContains(t, err, "only one of Azure or S3 can be configured for build context upload")
	})

	t.Run("azure without container", func(t *testing.T) {
		t.Parallel()

		b, err := NewK8sBuilder(context.Background(), Config{
			Context: Context{
				Azure: Azure{
					AccountName: "account_name",
				},
			},
		})
		require.Nil(t, b)
		require.ErrorContains(t, err, "creating context uploader: azure container name is required")
	})

	t.Run("azure without flags", func(t *testing.T) {
		t.Parallel()

		b, err := NewK8sBuilder(context.Background(), Config{
			Context: Context{
				Azure: Azure{
					AccountName: "account_name",
					Container:   "container",
				},
			},
		})
		require.NotNil(t, b)
		require.NoError(t, err)
		assert.Equal(t, defaultFlag, b.podConfig.Env["BUILDKITD_FLAGS"])
	})

	t.Run("azure with default flag", func(t *testing.T) {
		t.Parallel()

		b, err := NewK8sBuilder(context.Background(), Config{
			Context: Context{
				Azure: Azure{
					AccountName: "account",
					Container:   "container",
				},
			},
			Executor: Executor{
				PodConfig: k8sutils.PodConfig{
					Env: map[string]string{
						"BUILDKITD_FLAGS": defaultFlag,
					},
				},
			},
		})
		require.NotNil(t, b)
		require.NoError(t, err)
		assert.Equal(t, defaultFlag, b.podConfig.Env["BUILDKITD_FLAGS"])
	})

	t.Run("azure with extra flags", func(t *testing.T) {
		t.Parallel()

		b, err := NewK8sBuilder(context.Background(), Config{
			Context: Context{
				Azure: Azure{
					AccountName: "account",
					Container:   "container",
				},
			},
			Executor: Executor{
				PodConfig: k8sutils.PodConfig{
					Env: map[string]string{
						"BUILDKITD_FLAGS": "--test1 --test2",
					},
				},
			},
		})
		require.NotNil(t, b)
		require.NoError(t, err)
		assert.Equal(t,
			strings.Join([]string{"--test1 --test2", defaultFlag}, " "),
			b.podConfig.Env["BUILDKITD_FLAGS"])
	})
}

func TestK8sBuilder_Build(t *testing.T) {
	t.Parallel()

	const (
		//nolint:gosec
		dockerConfigSecret = "docker-config-secret"
	)

	testCases := []struct {
		name               string
		modifyOpts         func(opts *types.ImageBuilderOpts)
		modifyPodConfig    func(podConfig *k8sutils.PodConfig)
		expectedPodFunc    func(dockerConfigSecret string, podConfig k8sutils.PodConfig, args []string) (*corev1.Pod, error)
		dockerConfigSecret string
		expectedError      error
	}{
		{
			name:               "Executes",
			modifyOpts:         func(opts *types.ImageBuilderOpts) {},
			modifyPodConfig:    func(podConfig *k8sutils.PodConfig) {},
			dockerConfigSecret: dockerConfigSecret,
			expectedError:      nil,
		},
		{
			name: "ExecutesDisablePush",
			modifyOpts: func(opts *types.ImageBuilderOpts) {
				opts.Push = false
			},
			modifyPodConfig:    func(podConfig *k8sutils.PodConfig) {},
			dockerConfigSecret: dockerConfigSecret,
			expectedError:      nil,
		},
		{
			name: "ExecutesWithoutTags",
			modifyOpts: func(opts *types.ImageBuilderOpts) {
				opts.Tags = nil
			},
			modifyPodConfig:    func(podConfig *k8sutils.PodConfig) {},
			dockerConfigSecret: dockerConfigSecret,
			expectedError:      errors.New("at least one tag is required when using the Kubernetes executor"),
		},
		{
			name: "ExecutesWithFile",
			modifyOpts: func(opts *types.ImageBuilderOpts) {
				opts.File = filepath.Join(opts.Context, defaultDockerfileName)
			},
			modifyPodConfig:    func(podConfig *k8sutils.PodConfig) {},
			dockerConfigSecret: dockerConfigSecret,
			expectedError:      nil,
		},
		{
			name:       "ExecutesWithDiffrentNamespace",
			modifyOpts: func(opts *types.ImageBuilderOpts) {},
			modifyPodConfig: func(podConfig *k8sutils.PodConfig) {
				podConfig.Namespace = "test-namespace"
			},
			dockerConfigSecret: dockerConfigSecret,
			expectedError:      nil,
		},
		{
			name:               "FailsOnExecutorError",
			modifyOpts:         func(opts *types.ImageBuilderOpts) {},
			modifyPodConfig:    func(podConfig *k8sutils.PodConfig) {},
			dockerConfigSecret: "",
			expectedError:      errors.New("the DockerConfigSecret option is required"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts := provideDefaultOptions(t)
			tc.modifyOpts(&opts)

			podConfig := k8sutils.PodConfig{
				DockerConfigSecret: tc.dockerConfigSecret,
				// Use a fixed name generator for the pod to ensure the test is deterministic
				NameGenerator: func() string { return "test-pod-name" },
			}
			tc.modifyPodConfig(&podConfig)

			buildctlArgs, err := generateBuildctlArgs(opts)
			require.NoError(t, err)

			expectedPodFunc := func(podConfig k8sutils.PodConfig, args []string) (*corev1.Pod, error) {
				pod, err := buildPod(podConfig, args)
				return pod, err
			}
			pod, _ := expectedPodFunc(podConfig, buildctlArgs)
			fakeExecutor := mock.NewKubernetesExecutor(pod)

			b := K8sBuilder{
				executor:        fakeExecutor,
				podConfig:       podConfig,
				contextProvider: &mockContextProvider{opts.Context},
			}

			err = b.Build(context.Background(), opts)
			if tc.expectedError != nil {
				require.EqualError(t, err, tc.expectedError.Error())
			} else {
				require.NoError(t, err)
				// Skip the comparison of the pod's name and instance label
				// The test is still valid because we're checking that the executor was called with a pod
				// that has the correct configuration, except for the name and instance label
				// which are generated dynamically
				assert.NotNil(t, fakeExecutor.Applied)
			}
		})
	}
}

func provideDefaultOptions(t *testing.T) types.ImageBuilderOpts {
	t.Helper()
	// generate Dockerfile
	buildCtx := t.TempDir()
	dockerfile := fmt.Sprintf(`FROM %s
	CMD ["echo", "dib-build-test-string"]`, testutil.CommonImage)

	err := os.WriteFile(filepath.Join(buildCtx, defaultDockerfileName), []byte(dockerfile), 0o600)
	require.NoError(t, err)

	return types.ImageBuilderOpts{
		BuildkitHost: getBuildkitHostAddress(),
		Context:      buildCtx,
		Tags: []string{
			"gcr.io/project-id/image:version",
			"gcr.io/project-id/image:latest",
		},
		BuildArgs: map[string]string{
			"someArg": "someValue",
		},
		Labels: map[string]string{
			"someLabel": "someValue",
		},
		Push:      true,
		LogOutput: &bytes.Buffer{},
		Progress:  "auto",
	}
}

type mockContextProvider struct {
	context string
}

func (m *mockContextProvider) PrepareContext(_ context.Context, _ types.ImageBuilderOpts) (string, error) {
	return m.context, nil
}
