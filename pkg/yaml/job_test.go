package yaml

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadJobDefaultsGPUSelectorAndSlurm(t *testing.T) {
	data := []byte(`
apiVersion: batch/v1
kind: Job
metadata:
  name: cuda-add
spec:
  selector:
    matchLabels:
      accelerator: gpu
  nodeSelector:
    node: node-a
  source:
    files:
    - vector_add.cu
    - Makefile
    command: make run
  remote:
    host: sylogin.hpc.sjtu.edu.cn
    username: stu1718
    workdir: /dssg/home/acct-stu/stu1718/minik8s-gpujobs
`)

	job, err := LoadJobFromYAML(data)
	require.NoError(t, err)

	assert.Equal(t, "Job", job.Kind)
	assert.Equal(t, "batch/v1", job.APIVersion)
	assert.Equal(t, "default", job.Namespace)
	assert.Equal(t, "gpu", job.Spec.Selector.MatchLabels["accelerator"])
	assert.Equal(t, map[string]string{"node": "node-a"}, job.Spec.NodeSelector)
	assert.Equal(t, "debuga100", job.Spec.Slurm.Partition)
	assert.Equal(t, "debug", job.Spec.Slurm.QOS)
	assert.Equal(t, int32(1), job.Spec.Slurm.Nodes)
	assert.Equal(t, int32(1), job.Spec.Slurm.NTasksPerNode)
	assert.Equal(t, int32(4), job.Spec.Slurm.CPUsPerTask)
	assert.Equal(t, "gpu:1", job.Spec.Slurm.GRES)
	assert.Equal(t, "00:20:00", job.Spec.Slurm.Time)
}

func TestLoadJobRejectsNonGPUSelector(t *testing.T) {
	data := []byte(`
apiVersion: batch/v1
kind: Job
metadata:
  name: cpu-job
spec:
  selector:
    matchLabels:
      accelerator: cpu
  source:
    files: [main.c]
    command: make run
  remote:
    host: sylogin.hpc.sjtu.edu.cn
    username: stu1718
    workdir: /dssg/home/acct-stu/stu1718/minik8s-gpujobs
`)

	_, err := LoadJobFromYAML(data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accelerator")
}
