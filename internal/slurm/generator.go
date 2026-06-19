package slurm

import (
	"fmt"
	"regexp"
	"strings"

	"minik8s/internal/job"
)

var submittedJobPattern = regexp.MustCompile(`Submitted batch job\s+([0-9]+)`)

func GenerateScript(j *job.Job) string {
	spec := j.Spec.Slurm
	lines := []string{
		"#!/bin/bash -l",
		fmt.Sprintf("#SBATCH --job-name=%s", j.Name),
		fmt.Sprintf("#SBATCH --partition=%s", spec.Partition),
		fmt.Sprintf("#SBATCH --nodes=%d", spec.Nodes),
		fmt.Sprintf("#SBATCH --ntasks-per-node=%d", spec.NTasksPerNode),
		fmt.Sprintf("#SBATCH --cpus-per-task=%d", spec.CPUsPerTask),
		fmt.Sprintf("#SBATCH --gres=%s", spec.GRES),
		fmt.Sprintf("#SBATCH --time=%s", spec.Time),
		"#SBATCH --output=%j.out",
		"#SBATCH --error=%j.err",
		"",
	}
	if spec.QOS != "" {
		lines = append(lines[:3], append([]string{fmt.Sprintf("#SBATCH --qos=%s", spec.QOS)}, lines[3:]...)...)
	}
	lines = append(lines,
		"set -euo pipefail",
		"",
		`cd "$SLURM_SUBMIT_DIR"`,
		"",
		"module load cuda",
		"",
		`echo "===== Minik8s Job Runtime Info ====="`,
		`echo "Job ID: $SLURM_JOB_ID"`,
		`echo "Job Name: $SLURM_JOB_NAME"`,
		`echo "Node List: $SLURM_JOB_NODELIST"`,
		`echo "Submit Dir: $SLURM_SUBMIT_DIR"`,
		`echo "CUDA:"`,
		"which nvcc || true",
		"nvcc --version || true",
		`echo "GPU:"`,
		"nvidia-smi || true",
		`echo "===== User Command ====="`,
		"",
		j.Spec.Source.Command,
		"",
	)
	return strings.Join(lines, "\n")
}

func ParseSubmittedJobID(output string) (string, error) {
	matches := submittedJobPattern.FindStringSubmatch(output)
	if len(matches) != 2 {
		return "", fmt.Errorf("parsing sbatch output %q: submitted job id not found", strings.TrimSpace(output))
	}
	return matches[1], nil
}

func SlurmAccountingState(accounting, jobID string) string {
	for _, line := range strings.Split(accounting, "\n") {
		fields := strings.Split(line, "|")
		if len(fields) < 2 {
			continue
		}
		if strings.TrimSpace(fields[0]) == jobID {
			return strings.TrimSpace(fields[1])
		}
	}
	return ""
}
