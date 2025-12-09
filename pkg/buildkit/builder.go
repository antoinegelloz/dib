package buildkit

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/distribution/reference"
	"github.com/radiofrance/dib/pkg/buildcontext"
	"github.com/radiofrance/dib/pkg/exec"
	"github.com/radiofrance/dib/pkg/executor"
	k8sutils "github.com/radiofrance/dib/pkg/kubernetes"
	"github.com/radiofrance/dib/pkg/logger"
	"github.com/radiofrance/dib/pkg/strutil"
	"github.com/radiofrance/dib/pkg/types"
	"github.com/radiofrance/kubecli"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

type Builder struct {
	contextProvider buildcontext.ContextProvider
	shellExecutor   executor.ShellExecutor
	k8sExecutor     executor.KubernetesExecutor
	podConfig       k8sutils.PodConfig // The default pod configuration used to run Buildkit builds.
}

// Config holds the configuration for the Buildkit build backend.
type Config struct {
	Context  Context  `mapstructure:"context"`
	Executor Executor `mapstructure:"executor"`
}

// Executor holds the configuration for the executor.
type Executor struct {
	Kubernetes k8sutils.PodConfig `mapstructure:"kubernetes"`
}

// Context holds the configuration for the build context upload.
type Context struct {
	S3    S3    `mapstructure:"s3"`
	Azure Azure `mapstructure:"azure"`
}

// S3 holds the configuration for S3-compatible storage for build context upload.
type S3 struct {
	Bucket string `mapstructure:"bucket"`
	Region string `mapstructure:"region"`
}

// Azure holds the configuration for Azure Blob storage for build context upload.
type Azure struct {
	AccountName string `mapstructure:"account_name"`
	Container   string `mapstructure:"container"`
}

// NewShellBuilder creates a new instance of Builder to use the shell executor to build images.
func NewShellBuilder(shell executor.ShellExecutor) (*Builder, error) {
	return &Builder{
		shellExecutor:   shell,
		contextProvider: NewLocalContextProvider(),
	}, nil
}

// NewK8sBuilder creates a new instance of Builder to use the kubernetes executor to build images.
func NewK8sBuilder(ctx context.Context, cfg Config) (*Builder, error) {
	k8sExecutor, err := createK8sExecutor()
	if err != nil {
		return nil, fmt.Errorf("cannot create buildkit kubernetes executor: %w", err)
	}

	// ensure env map exists
	if cfg.Executor.Kubernetes.Env == nil {
		cfg.Executor.Kubernetes.Env = make(map[string]string)
	}

	// This flag is required to avoid creating a new PID namespace for the rootlesskit child
	// process and mounting the procfs, which is not possible. Sharing the host PID namespace
	// can be dangerous, but it is safe here as we run buildkitd in rootless mode.
	// Buildkit documentation recommends using `--oci-worker-no-process-sandbox` instead of
	// `securityContext.procMount=Unmasked` to unmask the host procfs.
	// see https://github.com/moby/buildkit/blob/master/docs/rootless.md#docker
	const flag = "--oci-worker-no-process-sandbox"

	existingFlags := cfg.Executor.Kubernetes.Env["BUILDKITD_FLAGS"]

	// split on any whitespace, trimming extra spaces
	flags := strings.Fields(existingFlags)

	// avoid adding a duplicate flag
	found := slices.Contains(flags, flag)

	if !found {
		flags = append(flags, flag)
	}

	cfg.Executor.Kubernetes.Env["BUILDKITD_FLAGS"] = strings.Join(flags, " ")

	var uploader buildcontext.FileUploader

	switch {
	case cfg.Context.Azure.AccountName != "" && cfg.Context.S3.Bucket != "":
		return nil, errors.New("only one of Azure or S3 can be configured for build context upload")
	case cfg.Context.Azure.AccountName != "":
		uploader, err = buildcontext.NewAzureUploader(cfg.Context.Azure.AccountName, cfg.Context.Azure.Container)
	case cfg.Context.S3.Bucket != "":
		uploader, err = buildcontext.NewS3Uploader(ctx, cfg.Context.S3.Region, cfg.Context.S3.Bucket)
	default:
		return nil, errors.New("either Azure or S3 must be configured for build context upload")
	}

	if err != nil {
		return nil, fmt.Errorf("creating context uploader: %w", err)
	}

	return &Builder{
		k8sExecutor:     k8sExecutor,
		podConfig:       cfg.Executor.Kubernetes,
		contextProvider: buildcontext.NewRemoteContextProvider(uploader, "buildkit"),
	}, nil
}

// Build the image using the Buildkit backend.
func (b *Builder) Build(ctx context.Context, opts types.ImageBuilderOpts) error {
	var err error

	opts.Context, err = b.contextProvider.PrepareContext(ctx, opts)
	if err != nil {
		return fmt.Errorf("cannot prepare buildkit build context: %w", err)
	}

	buildctlArgs, err := generateBuildctlArgs(opts)
	if err != nil {
		return err
	}

	// `shellExecutor` or `kubernetesExecutor` are mutually exclusive.
	if b.shellExecutor != nil {
		buildctlBinary, err := BuildctlBinary()
		if err != nil {
			return fmt.Errorf("cannot find buildctl binary: %w", err)
		}

		return b.shellExecutor.ExecuteStdout(buildctlBinary, buildctlArgs...)
	}

	if len(opts.Tags) == 0 {
		return errors.New("at least one tag is required when using the Kubernetes executor")
	}

	// Parse the first tag to get a normalized reference
	parsedReference, err := reference.ParseNormalizedNamed(opts.Tags[0])
	if err != nil {
		return fmt.Errorf("failed to parse image reference: %w", err)
	}

	// Get the familiar name (repository without tag)
	imageName := reference.FamiliarName(parsedReference)

	// Extract just the last part of the repository path
	if idx := strings.LastIndex(imageName, "/"); idx > 0 {
		imageName = imageName[idx+1:]
	}

	// Make a copy of the pod config to prevent concurrent modifications to the original
	podConfig := b.podConfig
	podConfig.NameGenerator = k8sutils.UniquePodNameWithImage("dib-buildkit", imageName)

	logger.Debugf("Building pod with config: %+v buildctlArgs: %+v", podConfig, buildctlArgs)

	pod, err := buildPod(podConfig, buildctlArgs)
	if err != nil {
		return err
	}

	logger.Infof(`Starting pod "%s/%s" to build image %q`, pod.Namespace, pod.Name, imageName)

	err = b.k8sExecutor.ApplyWithWriters(ctx,
		opts.LogOutput, opts.LogOutput, pod, "buildkit")
	if err != nil {
		return err
	}

	return nil
}

func createK8sExecutor() (*exec.KubernetesExecutor, error) {
	k8sClient, err := kubecli.New("")
	if err != nil {
		return nil, fmt.Errorf("could not get kube client from context: %w", err)
	}

	return exec.NewKubernetesExecutor(k8sClient.ClientSet), nil
}

func generateBuildctlArgs(opts types.ImageBuilderOpts) ([]string, error) {
	var output strings.Builder
	output.WriteString("type=image,unpack=true")

	if tags := strutil.DedupeStrSlice(opts.Tags); len(tags) > 0 {
		for _, tag := range tags {
			// Normalize the tag by transforming it from a familiar name used in Docker UI to a fully qualified reference.
			parsedReference, err := reference.ParseNormalizedNamed(tag)
			if err != nil {
				return nil, err
			}

			output.WriteString(",name=" + parsedReference.String())
		}
	} else {
		output.WriteString(",dangling-name-prefix=<none>")
	}

	if !opts.LocalOnly || opts.Push {
		output.WriteString(",push=true")
	}

	buildctlArgs := buildctlBaseArgs(opts.BuildkitHost)

	var contextArg string
	if opts.LocalOnly {
		contextArg = "--local=context=" + opts.Context
	} else {
		// We use a pre-signed URL to securely fetch the context from a remote source, ensuring proper access control.
		contextArg = "--opt=context=" + opts.Context
	}

	buildctlArgs = append(buildctlArgs, []string{
		"build",
		"--progress=" + opts.Progress,
		"--frontend=dockerfile.v0",
		contextArg,
		"--output=" + output.String(),
	}...)

	if opts.LocalOnly {
		// Set the directory and filename for the Dockerfile,
		// as the Dockerfile path may differ from the build context path.
		dir := opts.Context

		file := defaultDockerfileName
		if opts.File != "" {
			dir, file = filepath.Split(opts.File)

			if dir == "" {
				dir = "."
			}
		}

		var err error

		dir, file, err = buildKitFile(dir, file)
		if err != nil {
			return nil, err
		}

		buildctlArgs = append(buildctlArgs, "--local=dockerfile="+dir)
		buildctlArgs = append(buildctlArgs, "--opt=filename="+file)
	}

	// The target option specifies the build stage to build.
	if opts.Target != "" {
		buildctlArgs = append(buildctlArgs, "--opt=target="+opts.Target)
	}

	for key, val := range opts.BuildArgs {
		buildctlArgs = append(buildctlArgs, "--opt=build-arg:"+key+"="+val)
	}

	for k, v := range opts.Labels {
		buildctlArgs = append(buildctlArgs, "--opt=label:"+k+"="+v)
	}

	return buildctlArgs, nil
}

func buildPod(podConfig k8sutils.PodConfig, args []string) (*corev1.Pod, error) {
	if podConfig.DockerConfigSecret == "" {
		return nil, errors.New("the DockerConfigSecret option is required")
	}

	podName := podConfig.Name
	if podConfig.NameGenerator != nil {
		podName = podConfig.NameGenerator()
	}

	containerName := "buildkit"

	labels := map[string]string{
		"app.kubernetes.io/name":      "buildkit",
		"app.kubernetes.io/component": "build-pod",
		"app.kubernetes.io/instance":  podName,
	}
	// Merge the default labels with those provided in the options.
	maps.Copy(labels, podConfig.Labels)

	objectMeta := metav1.ObjectMeta{
		Name:      podName,
		Namespace: podConfig.Namespace,
		Labels:    labels,
	}

	var imagePullSecrets []corev1.LocalObjectReference
	for _, secretName := range podConfig.ImagePullSecrets {
		imagePullSecrets = append(imagePullSecrets, corev1.LocalObjectReference{
			Name: secretName,
		})
	}

	var envVars []corev1.EnvVar
	for k, v := range podConfig.Env {
		envVars = append(envVars, corev1.EnvVar{
			Name:  k,
			Value: v,
		})
	}

	var envFrom []corev1.EnvFromSource
	for _, secretName := range podConfig.EnvSecrets {
		envFrom = append(envFrom, corev1.EnvFromSource{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: secretName,
				},
			},
		})
	}

	container := corev1.Container{
		Name:            containerName,
		Image:           podConfig.Image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Args:            args,
		EnvFrom:         envFrom,
		Env: append([]corev1.EnvVar{
			{
				Name:  "DOCKER_CONFIG",
				Value: "/buildkit/.docker",
			},
		}, envVars...),
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      podConfig.DockerConfigSecret,
				MountPath: "/buildkit/.docker",
				ReadOnly:  true,
			},
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"buildctl", "debug", "workers"},
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       30,
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"buildctl", "debug", "workers"},
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       30,
		},
		SecurityContext: &corev1.SecurityContext{
			RunAsUser:  ptr.To[int64](RemoteUserId),
			RunAsGroup: ptr.To[int64](RemoteGroupId),
			// Needs Kubernetes >= 1.19
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeUnconfined,
			},
			// Needs Kubernetes >= 1.30
			// https://github.com/rootless-containers/rootlesskit/pull/421
			AppArmorProfile: &corev1.AppArmorProfile{
				Type: corev1.AppArmorProfileTypeUnconfined,
			},
		},
	}

	err := k8sutils.MergeObjectWithYaml(&container, podConfig.ContainerOverride)
	if err != nil {
		return nil, err
	}

	pod := corev1.Pod{
		ObjectMeta: objectMeta,
		Spec: corev1.PodSpec{
			ImagePullSecrets: imagePullSecrets,
			Containers: []corev1.Container{
				container,
			},
			RestartPolicy: corev1.RestartPolicyNever,
			Affinity: &corev1.Affinity{
				PodAntiAffinity: &corev1.PodAntiAffinity{
					PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
						{
							PodAffinityTerm: corev1.PodAffinityTerm{
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"app.kubernetes.io/name":      "buildkit",
										"app.kubernetes.io/component": "build-pod",
									},
								},
								TopologyKey: "kubernetes.io/hostname",
							},
							Weight: 50,
						},
						{
							PodAffinityTerm: corev1.PodAffinityTerm{
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"app.kubernetes.io/name":      "buildkit",
										"app.kubernetes.io/component": "build-pod",
									},
								},
								TopologyKey: "topology.kubernetes.io/zone",
							},
							Weight: 100,
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: podConfig.DockerConfigSecret,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName:  podConfig.DockerConfigSecret,
							DefaultMode: ptr.To[int32](420),
						},
					},
				},
			},
		},
	}

	err = k8sutils.MergeObjectWithYaml(&pod, podConfig.PodOverride)
	if err != nil {
		return nil, err
	}

	return &pod, nil
}
