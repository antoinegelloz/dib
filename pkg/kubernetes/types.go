//nolint:lll
package kubernetes

// PodConfig hold the configuration for the kubernetes pod to create.
type PodConfig struct {
	// Name of the secret containing the docker config used by Buildkit (required).
	DockerConfigSecret string `mapstructure:"docker_config_secret"`

	Name             string            // The name of the pod. Must be unique to avoid collisions with an existing pod.
	NameGenerator    func() string     `yaml:"-"`                 // A function that generates the pod name. Will override the Name option.
	Namespace        string            `mapstructure:"namespace"` // The namespace where the pod should be created.
	Labels           map[string]string // A map of key/value labels.
	Image            string            `mapstructure:"image"`              // The image for the container.
	ImagePullSecrets []string          `mapstructure:"image_pull_secrets"` // A list of `imagePullSecret` secret names.
	Env              map[string]string `mapstructure:"env"`                // A map of key/value env variables.
	EnvSecrets       []string          `mapstructure:"env_secrets"`        // A list of `envFrom` secret names.

	// Advanced customizations (raw YAML overrides)
	ContainerOverride string `mapstructure:"container_override"`    // YAML string to override the container object.
	PodOverride       string `mapstructure:"pod_template_override"` // YAML string to override the pod object.
}
