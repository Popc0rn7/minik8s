package slurm

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"minik8s/internal/job"
)

type Transport interface {
	Run(ctx context.Context, host, username, command string) (string, error)
	Upload(ctx context.Context, host, username, localPath, remotePath string) error
	Download(ctx context.Context, host, username, remotePath, localPath string) error
}

type SSHTransport struct{}

func (SSHTransport) Run(ctx context.Context, host, username, command string) (string, error) {
	target := username + "@" + host
	out, err := exec.CommandContext(ctx, "ssh", target, command).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("ssh %s: %w: %s", target, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (SSHTransport) Upload(ctx context.Context, host, username, localPath, remotePath string) error {
	target := username + "@" + host + ":" + remotePath
	out, err := exec.CommandContext(ctx, "scp", localPath, target).CombinedOutput()
	if err != nil {
		return fmt.Errorf("scp upload %s: %w: %s", target, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (SSHTransport) Download(ctx context.Context, host, username, remotePath, localPath string) error {
	source := username + "@" + host + ":" + remotePath
	out, err := exec.CommandContext(ctx, "scp", source, localPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("scp download %s: %w: %s", source, err, strings.TrimSpace(string(out)))
	}
	return nil
}

type Runner struct {
	Transport Transport
	PollEvery time.Duration
}

type Result struct {
	SlurmJobID string
	RemoteDir  string
	Output     string
	Error      string
}

func (r Runner) Run(ctx context.Context, j *job.Job, workspace string, update func(job.JobStatus) error) (Result, error) {
	transport := r.Transport
	if transport == nil {
		transport = SSHTransport{}
	}
	pollEvery := r.PollEvery
	if pollEvery == 0 {
		pollEvery = 10 * time.Second
	}
	if err := writeArtifacts(j, workspace); err != nil {
		return Result{}, err
	}
	scriptPath := filepath.Join(workspace, "job.slurm")
	if err := os.WriteFile(scriptPath, []byte(GenerateScript(j)), 0o644); err != nil {
		return Result{}, fmt.Errorf("writing slurm script: %w", err)
	}
	remoteDir := strings.TrimRight(j.Spec.Remote.Workdir, "/") + "/" + j.Name + "-" + time.Now().UTC().Format("20060102150405")
	status := j.Status
	status.Phase = job.JobPreparing
	status.RemoteDir = remoteDir
	status.Message = "preparing local workspace"
	_ = update(status)
	if _, err := transport.Run(ctx, j.Spec.Remote.Host, j.Spec.Remote.Username, "mkdir -p "+shellQuote(remoteDir)); err != nil {
		return Result{}, err
	}
	status.Phase = job.JobUploading
	status.Message = "uploading source files"
	_ = update(status)
	for _, artifact := range j.Spec.Source.Artifacts {
		clean := filepath.Clean(artifact.Path)
		local := filepath.Join(workspace, clean)
		remote := remoteDir + "/" + filepath.ToSlash(clean)
		if dir := filepath.ToSlash(filepath.Dir(clean)); dir != "." {
			if _, err := transport.Run(ctx, j.Spec.Remote.Host, j.Spec.Remote.Username, "mkdir -p "+shellQuote(remoteDir+"/"+dir)); err != nil {
				return Result{}, err
			}
		}
		if err := transport.Upload(ctx, j.Spec.Remote.Host, j.Spec.Remote.Username, local, remote); err != nil {
			return Result{}, err
		}
	}
	if err := transport.Upload(ctx, j.Spec.Remote.Host, j.Spec.Remote.Username, scriptPath, remoteDir+"/job.slurm"); err != nil {
		return Result{}, err
	}
	out, err := transport.Run(ctx, j.Spec.Remote.Host, j.Spec.Remote.Username, "cd "+shellQuote(remoteDir)+" && sbatch job.slurm")
	if err != nil {
		return Result{}, err
	}
	slurmID, err := ParseSubmittedJobID(out)
	if err != nil {
		return Result{}, err
	}
	status.Phase = job.JobSubmitted
	status.SlurmJobID = slurmID
	status.Message = "slurm job submitted"
	_ = update(status)
	for {
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-time.After(pollEvery):
		}
		queue, err := transport.Run(ctx, j.Spec.Remote.Host, j.Spec.Remote.Username, "squeue -j "+shellQuote(slurmID)+" -h -o %T")
		if err != nil {
			return Result{}, err
		}
		if strings.TrimSpace(queue) == "" {
			break
		}
		status.Phase = job.JobRunning
		status.Message = "slurm job is active: " + strings.TrimSpace(queue)
		_ = update(status)
	}
	status.Phase = job.JobCollecting
	status.Message = "collecting slurm result"
	_ = update(status)
	accounting, err := transport.Run(ctx, j.Spec.Remote.Host, j.Spec.Remote.Username, "sacct -j "+shellQuote(slurmID)+" --format=JobID,State,ExitCode -P -n")
	if err != nil {
		return Result{}, err
	}
	stdoutPath := filepath.Join(workspace, slurmID+".out")
	stderrPath := filepath.Join(workspace, slurmID+".err")
	_ = transport.Download(ctx, j.Spec.Remote.Host, j.Spec.Remote.Username, remoteDir+"/"+slurmID+".out", stdoutPath)
	_ = transport.Download(ctx, j.Spec.Remote.Host, j.Spec.Remote.Username, remoteDir+"/"+slurmID+".err", stderrPath)
	stdout, _ := os.ReadFile(stdoutPath)
	stderr, _ := os.ReadFile(stderrPath)
	result := Result{SlurmJobID: slurmID, RemoteDir: remoteDir, Output: string(stdout), Error: string(stderr)}
	state := SlurmAccountingState(accounting, slurmID)
	if state == "COMPLETED" {
		status.Phase = job.JobSucceeded
		status.Message = "job completed successfully"
	} else {
		status.Phase = job.JobFailed
		status.Message = "job failed: " + strings.TrimSpace(accounting)
	}
	status.LastOutput = result.Output
	status.LastError = result.Error
	status.CompletionTime = time.Now().UTC()
	_ = update(status)
	return result, nil
}

func writeArtifacts(j *job.Job, workspace string) error {
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return fmt.Errorf("creating job workspace: %w", err)
	}
	for _, artifact := range j.Spec.Source.Artifacts {
		data, err := base64.StdEncoding.DecodeString(artifact.Content)
		if err != nil {
			return fmt.Errorf("decoding artifact %s: %w", artifact.Path, err)
		}
		clean := filepath.Clean(artifact.Path)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("artifact path %q escapes workspace", artifact.Path)
		}
		dst := filepath.Join(workspace, clean)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return fmt.Errorf("writing artifact %s: %w", artifact.Path, err)
		}
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
