package swarm

import (
	"reflect"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/swarm"
)

func TestAdjustForAPIVersion(t *testing.T) {
	expectedSysctls := map[string]string{"foo": "bar"}
	// testing the negative -- does this leave everything else alone? -- is
	// prohibitively time-consuming to write, because it would need an object
	// with literally every field filled in.
	spec := &swarm.ServiceSpec{
		TaskTemplate: swarm.TaskSpec{
			ContainerSpec: &swarm.ContainerSpec{
				Sysctls: expectedSysctls,
				Privileges: &swarm.Privileges{
					CredentialSpec: &swarm.CredentialSpec{
						Config: "someconfig",
					},
				},
				Configs: []*swarm.ConfigReference{
					{
						File: &swarm.ConfigReferenceFileTarget{
							Name: "foo",
							UID:  "bar",
							GID:  "baz",
						},
						ConfigID:   "configFile",
						ConfigName: "configFile",
					},
					{
						Runtime:    &swarm.ConfigReferenceRuntimeTarget{},
						ConfigID:   "configRuntime",
						ConfigName: "configRuntime",
					},
				},
				Ulimits: []*container.Ulimit{
					{
						Name: "nofile",
						Soft: 100,
						Hard: 200,
					},
				},
				Mounts: []mount.Mount{
					{
						Type:   mount.TypeTmpfs,
						Source: "/foo",
						Target: "/bar",
						TmpfsOptions: &mount.TmpfsOptions{
							Options: [][]string{
								{"exec"},
							},
						},
					},
				},
			},
			Placement: &swarm.Placement{
				MaxReplicas: 222,
			},
			Resources: &swarm.ResourceRequirements{
				Limits: &swarm.Limit{
					Pids: 300,
				},
			},
		},
	}

	adjustForAPIVersion("1.46", spec)
	if !reflect.DeepEqual(
		spec.TaskTemplate.ContainerSpec.Mounts[0].TmpfsOptions.Options,
		[][]string{{"exec"}},
	) {
		t.Error("TmpfsOptions.Options was stripped from spec")
	}

	adjustForAPIVersion("1.45", spec)
	if len(spec.TaskTemplate.ContainerSpec.Mounts[0].TmpfsOptions.Options) != 0 {
		t.Error("TmpfsOptions.Options not stripped from spec")
	}

	// first, does calling this with a later version correctly NOT strip
	// fields? do the later version first, so we can reuse this spec in the
	// next test.
	adjustForAPIVersion("1.41", spec)
	if !reflect.DeepEqual(spec.TaskTemplate.ContainerSpec.Sysctls, expectedSysctls) {
		t.Error("Sysctls was stripped from spec")
	}

	if spec.TaskTemplate.Resources.Limits.Pids == 0 {
		t.Error("PidsLimit was stripped from spec")
	}
	if spec.TaskTemplate.Resources.Limits.Pids != 300 {
		t.Error("PidsLimit did not preserve the value from spec")
	}

	if spec.TaskTemplate.ContainerSpec.Privileges.CredentialSpec.Config != "someconfig" {
		t.Error("CredentialSpec.Config field was stripped from spec")
	}

	if spec.TaskTemplate.ContainerSpec.Configs[1].Runtime == nil {
		t.Error("ConfigReferenceRuntimeTarget was stripped from spec")
	}

	if spec.TaskTemplate.Placement.MaxReplicas != 222 {
		t.Error("MaxReplicas was stripped from spec")
	}

	if len(spec.TaskTemplate.ContainerSpec.Ulimits) == 0 {
		t.Error("Ulimits were stripped from spec")
	}

	// next, does calling this with an earlier version correctly strip fields?
	adjustForAPIVersion("1.29", spec)
	if spec.TaskTemplate.ContainerSpec.Sysctls != nil {
		t.Error("Sysctls was not stripped from spec")
	}

	if spec.TaskTemplate.Resources.Limits.Pids != 0 {
		t.Error("PidsLimit was not stripped from spec")
	}

	if spec.TaskTemplate.ContainerSpec.Privileges.CredentialSpec.Config != "" {
		t.Error("CredentialSpec.Config field was not stripped from spec")
	}

	if spec.TaskTemplate.ContainerSpec.Configs[1].Runtime != nil {
		t.Error("ConfigReferenceRuntimeTarget was not stripped from spec")
	}

	if spec.TaskTemplate.Placement.MaxReplicas != 0 {
		t.Error("MaxReplicas was not stripped from spec")
	}

	if len(spec.TaskTemplate.ContainerSpec.Ulimits) != 0 {
		t.Error("Ulimits were not stripped from spec")
	}
}
