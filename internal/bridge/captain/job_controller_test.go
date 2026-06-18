package captain

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/job"
	"minik8s/internal/node"
	"minik8s/internal/pod"
)

func TestJobControllerCreatesSubmitterPodAndService(t *testing.T) {
	pods := store.NewInMemoryPodStore()
	services := store.NewInMemoryServiceStore()
	jobs := store.NewInMemoryJobStore()
	j := &job.Job{
		TypeMeta: pod.TypeMeta{Kind: "Job", APIVersion: "batch/v1"},
		ObjectMeta: pod.ObjectMeta{
			Name:      "cuda-add",
			Namespace: "default",
		},
		Spec: job.JobSpec{
			Selector:     pod.LabelSelector{MatchLabels: map[string]string{"accelerator": "gpu"}},
			NodeSelector: map[string]string{"node": "node-a"},
			Source:       job.JobSourceSpec{Files: []string{"vector_add.cu", "Makefile"}, Command: "make run"},
			Slurm:        job.JobSlurmSpec{Partition: "debuga100"},
			Remote:       job.JobRemoteSpec{Host: "sylogin.hpc.sjtu.edu.cn", Username: "stu1718", Workdir: "/dssg/home/acct-stu/stu1718/minik8s-gpujobs"},
		},
		Status: job.JobStatus{Phase: job.JobPending},
	}
	require.NoError(t, jobs.Create(j))

	nodes := store.NewInMemoryNodeStore()
	require.NoError(t, nodes.Upsert(node.New("node-a", node.NodeSpec{}, node.NodeStatus{
		Addresses: []node.NodeAddress{{Type: node.NodeAddressInternalIP, Address: "10.119.15.146"}},
	})))
	ctrl := NewJobController(pods, services, jobs, JobControllerConfig{
		SubmitterImage: "ghcr.io/popc0rn7/gpu-submitter:test",
		HarborURL:      "http://127.0.0.1:18080",
		NodeStore:      nodes,
		SSHHostPath:    "/opt/minik8s/.minik8s/secrets/gpu-ssh",
	})
	require.NoError(t, ctrl.Sync(context.Background()))

	p, err := pods.Get("job-cuda-add-submitter", "default")
	require.NoError(t, err)
	assert.Equal(t, "cuda-add", p.Labels[job.OwnerLabel])
	assert.Equal(t, "gpu", p.Labels["accelerator"])
	require.Len(t, p.Spec.Containers, 1)
	assert.Equal(t, "ghcr.io/popc0rn7/gpu-submitter:test", p.Spec.Containers[0].Image)
	assert.Equal(t, pod.RestartPolicyNever, p.Spec.RestartPolicy)
	assert.Equal(t, map[string]string{"node": "node-a"}, p.Spec.NodeSelector)
	assert.Contains(t, p.Spec.Containers[0].Args, "http://10.119.15.146:18080")
	assert.Equal(t, []pod.VolumeSpec{{
		Name:     "gpu-ssh",
		HostPath: &pod.HostPathVolume{Path: "/opt/minik8s/.minik8s/secrets/gpu-ssh", Type: "Directory"},
	}}, p.Spec.Volumes)
	assert.Contains(t, p.Spec.Containers[0].VolumeMounts, pod.VolumeMount{Name: "gpu-ssh", MountPath: "/root/.ssh", ReadOnly: true})

	svc, err := services.Get("job-cuda-add-submitter", "default")
	require.NoError(t, err)
	assert.Equal(t, "cuda-add", svc.Labels[job.OwnerLabel])

	updated, err := jobs.Get("cuda-add", "default")
	require.NoError(t, err)
	assert.Equal(t, job.JobPodCreating, updated.Status.Phase)
	assert.Equal(t, "job-cuda-add-submitter", updated.Status.SubmitterPod)
	assert.Equal(t, "job-cuda-add-submitter", updated.Status.SubmitterService)
}

func TestJobControllerDefaultSubmitterImageIsPinned(t *testing.T) {
	ctrl := NewJobController(
		store.NewInMemoryPodStore(),
		store.NewInMemoryServiceStore(),
		store.NewInMemoryJobStore(),
		JobControllerConfig{},
	)

	assert.Equal(t, "ghcr.io/popc0rn7/gpu-submitter:v0.1.0", ctrl.config.SubmitterImage)
	assert.Equal(t, "/opt/minik8s/secrets/gpu-ssh", ctrl.config.SSHHostPath)
}
