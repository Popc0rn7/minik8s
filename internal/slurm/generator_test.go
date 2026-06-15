package slurm

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"minik8s/internal/job"
	"minik8s/internal/pod"
)

func TestGenerateScriptUsesGPUJobFields(t *testing.T) {
	j := &job.Job{
		ObjectMeta: pod.ObjectMeta{Name: "cuda-add"},
		Spec: job.JobSpec{
			Source: job.JobSourceSpec{Command: "make run"},
			Slurm: job.JobSlurmSpec{
				Partition:     "debuga100",
				QOS:           "debug",
				Nodes:         1,
				NTasksPerNode: 1,
				CPUsPerTask:   4,
				GRES:          "gpu:1",
				Time:          "00:20:00",
			},
		},
	}

	script := GenerateScript(j)

	assert.Contains(t, script, "#!/bin/bash -l")
	assert.Contains(t, script, "set -euo pipefail")
	assert.Contains(t, script, "#SBATCH --job-name=cuda-add")
	assert.Contains(t, script, "#SBATCH --partition=debuga100")
	assert.Contains(t, script, "#SBATCH --qos=debug")
	assert.Contains(t, script, "#SBATCH --gres=gpu:1")
	assert.Contains(t, script, "module load cuda")
	assert.Contains(t, script, "nvidia-smi || true")
	assert.Contains(t, script, "make run")
	assert.NotContains(t, script, "--nodelist")
}

func TestParseSubmittedJobID(t *testing.T) {
	id, err := ParseSubmittedJobID("Submitted batch job 123456\n")
	assert.NoError(t, err)
	assert.Equal(t, "123456", id)
}

func TestSlurmAccountingStatusUsesMainJobLine(t *testing.T) {
	accounting := "58995629|FAILED|2:0\n58995629.batch|FAILED|2:0\n58995629.extern|COMPLETED|0:0\n"

	state := SlurmAccountingState(accounting, "58995629")

	assert.Equal(t, "FAILED", state)
}
