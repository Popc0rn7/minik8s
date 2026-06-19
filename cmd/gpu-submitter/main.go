package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"minik8s/internal/job"
	"minik8s/internal/slurm"
)

func main() {
	var harbor, namespace, name, workspaceRoot string
	flag.StringVar(&harbor, "harbor", os.Getenv("MINIK8S_HARBOR"), "Harbor API base URL")
	flag.StringVar(&namespace, "namespace", "default", "Job namespace")
	flag.StringVar(&name, "job", "", "Job name")
	flag.StringVar(&workspaceRoot, "workspace", "/var/lib/minik8s/jobs", "Local workspace root")
	flag.Parse()
	if harbor == "" || name == "" {
		fmt.Fprintln(os.Stderr, "gpu-submitter requires --harbor and --job")
		os.Exit(2)
	}
	ctx := context.Background()
	client := &apiClient{base: harbor, http: http.DefaultClient}
	j, err := client.getJob(ctx, namespace, name)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	workspace := filepath.Join(workspaceRoot, namespace+"-"+name)
	runner := slurm.Runner{PollEvery: 10 * time.Second}
	_, err = runner.Run(ctx, j, workspace, func(status job.JobStatus) error {
		_, updateErr := client.updateStatus(ctx, namespace, name, status)
		return updateErr
	})
	if err != nil {
		j.Status.Phase = job.JobFailed
		j.Status.Message = err.Error()
		j.Status.CompletionTime = time.Now().UTC()
		_, _ = client.updateStatus(ctx, namespace, name, j.Status)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type apiClient struct {
	base string
	http *http.Client
}

func (c *apiClient) getJob(ctx context.Context, namespace, name string) (*job.Job, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v1/namespaces/"+namespace+"/jobs/"+name, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get job %s/%s: %s", namespace, name, resp.Status)
	}
	var j job.Job
	if err := json.NewDecoder(resp.Body).Decode(&j); err != nil {
		return nil, err
	}
	return &j, nil
}

func (c *apiClient) updateStatus(ctx context.Context, namespace, name string, status job.JobStatus) (*job.Job, error) {
	data, err := json.Marshal(status)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.base+"/api/v1/namespaces/"+namespace+"/jobs/"+name+"/status", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update job %s/%s status: %s", namespace, name, resp.Status)
	}
	var j job.Job
	if err := json.NewDecoder(resp.Body).Decode(&j); err != nil {
		return nil, err
	}
	return &j, nil
}
