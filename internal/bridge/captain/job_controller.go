package captain

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/job"
	"minik8s/internal/minilog"
	"minik8s/internal/pod"
	"minik8s/internal/service"
	"minik8s/internal/slurm"
)

const JobControllerName = "job-controller"

type JobControllerConfig struct {
	SubmitterImage string
	HarborURL      string
	NodeStore      store.NodeStore
	SSHHostPath    string
}

type JobController struct {
	pods     store.PodStore
	services store.ServiceStore
	jobs     store.JobStore
	config   JobControllerConfig
}

func NewJobController(pods store.PodStore, services store.ServiceStore, jobs store.JobStore, config JobControllerConfig) *JobController {
	if config.SubmitterImage == "" {
		config.SubmitterImage = "ghcr.io/popc0rn7/gpu-submitter:v0.1.0"
	}
	if config.HarborURL == "" {
		config.HarborURL = "http://127.0.0.1:18080"
	}
	if config.SSHHostPath == "" {
		config.SSHHostPath = "/opt/minik8s/secrets/gpu-ssh"
	}
	return &JobController{pods: pods, services: services, jobs: jobs, config: config}
}

func (c *JobController) Name() string { return JobControllerName }

func (c *JobController) Sync(ctx context.Context) error {
	if c.pods == nil || c.services == nil || c.jobs == nil {
		return fmt.Errorf("job controller stores are required")
	}
	jobs, err := c.jobs.List("", nil)
	if err != nil {
		return fmt.Errorf("listing jobs: %w", err)
	}
	sort.Slice(jobs, func(i, k int) bool {
		if jobs[i].Namespace == jobs[k].Namespace {
			return jobs[i].Name < jobs[k].Name
		}
		return jobs[i].Namespace < jobs[k].Namespace
	})
	for _, j := range jobs {
		if err := c.reconcileJob(ctx, j); err != nil {
			return err
		}
	}
	return nil
}

func (c *JobController) DeleteJob(ctx context.Context, name, namespace string) error {
	j, err := c.jobs.Get(name, namespace)
	if err != nil {
		return err
	}
	if j.Status.SlurmJobID != "" {
		if _, cancelErr := (slurm.SSHTransport{}).Run(ctx, j.Spec.Remote.Host, j.Spec.Remote.Username, "scancel "+j.Status.SlurmJobID); cancelErr != nil {
			minilog.Warn("job-cancel", "job=%s/%s slurmJobID=%s error=%v", j.Namespace, j.Name, j.Status.SlurmJobID, cancelErr)
		}
	}
	submitterName := submitterName(j.Name)
	if err := c.pods.Delete(submitterName, j.Namespace); err != nil && err != store.ErrPodNotFound {
		return fmt.Errorf("deleting job submitter pod: %w", err)
	}
	if err := c.services.Delete(submitterName, j.Namespace); err != nil && err != store.ErrServiceNotFound {
		return fmt.Errorf("deleting job submitter service: %w", err)
	}
	if err := c.jobs.Delete(name, namespace); err != nil {
		return err
	}
	_ = ctx
	minilog.Info("job-delete", "job=%s/%s", j.Namespace, j.Name)
	return nil
}

func (c *JobController) reconcileJob(ctx context.Context, j *job.Job) error {
	switch j.Status.Phase {
	case job.JobSucceeded, job.JobFailed, job.JobCancelling:
		return nil
	}
	name := submitterName(j.Name)
	if _, err := c.pods.Get(name, j.Namespace); err != nil {
		if err != store.ErrPodNotFound {
			return fmt.Errorf("getting submitter pod for job %s/%s: %w", j.Namespace, j.Name, err)
		}
		if err := c.pods.Create(c.submitterPod(j, name)); err != nil && err != store.ErrPodAlreadyExists {
			return fmt.Errorf("creating submitter pod for job %s/%s: %w", j.Namespace, j.Name, err)
		}
	}
	if _, err := c.services.Get(name, j.Namespace); err != nil {
		if err != store.ErrServiceNotFound {
			return fmt.Errorf("getting submitter service for job %s/%s: %w", j.Namespace, j.Name, err)
		}
		if err := c.services.Create(c.submitterService(j, name)); err != nil && err != store.ErrServiceAlreadyExists {
			return fmt.Errorf("creating submitter service for job %s/%s: %w", j.Namespace, j.Name, err)
		}
	}
	if j.Status.Phase == "" || j.Status.Phase == job.JobPending {
		j.Status.Phase = job.JobPodCreating
		j.Status.SubmitterPod = name
		j.Status.SubmitterService = name
		j.Status.Message = "submitter pod and service created"
		if j.Status.StartTime.IsZero() {
			j.Status.StartTime = time.Now().UTC()
		}
		if err := c.jobs.Update(j); err != nil {
			return fmt.Errorf("updating job status: %w", err)
		}
	}
	_ = ctx
	minilog.Info("job-sync", "job=%s/%s phase=%s", j.Namespace, j.Name, j.Status.Phase)
	return nil
}

func (c *JobController) submitterPod(j *job.Job, name string) *pod.Pod {
	labels := submitterLabels(j)
	harborURL := c.harborURL()
	args := []string{"--job", j.Name, "--namespace", j.Namespace, "--harbor", harborURL}
	return &pod.Pod{
		TypeMeta: pod.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{
			Name:      name,
			Namespace: j.Namespace,
			Labels:    labels,
		},
		Spec: pod.PodSpec{
			RestartPolicy: pod.RestartPolicyNever,
			NodeSelector:  j.Spec.NodeSelector,
			Volumes: []pod.VolumeSpec{{
				Name:     "gpu-ssh",
				HostPath: &pod.HostPathVolume{Path: c.config.SSHHostPath, Type: "Directory"},
			}},
			Containers: []pod.ContainerSpec{{
				Name:    "submitter",
				Image:   c.config.SubmitterImage,
				Command: []string{"/usr/local/bin/gpu-submitter"},
				Args:    args,
				Env: []pod.EnvVar{{
					Name:  "MINIK8S_HARBOR",
					Value: harborURL,
				}},
				VolumeMounts: []pod.VolumeMount{{
					Name:      "gpu-ssh",
					MountPath: "/root/.ssh",
					ReadOnly:  true,
				}},
				Ports: []pod.ContainerPort{{
					Name:          "http",
					ContainerPort: 8080,
					Protocol:      "TCP",
				}},
			}},
		},
		Status: pod.PodStatus{Phase: pod.PodPending},
	}
}

func (c *JobController) harborURL() string {
	raw := strings.TrimSpace(c.config.HarborURL)
	if raw == "" {
		raw = "http://127.0.0.1:18080"
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || !isLoopbackHost(parsed.Hostname()) {
		return raw
	}
	nodeIP := c.controlPlaneIP()
	if nodeIP == "" {
		return raw
	}
	port := parsed.Port()
	if port == "" {
		port = "18080"
	}
	parsed.Host = net.JoinHostPort(nodeIP, port)
	return parsed.String()
}

func (c *JobController) controlPlaneIP() string {
	if c.config.NodeStore == nil {
		return ""
	}
	nodes, err := c.config.NodeStore.List()
	if err != nil {
		return ""
	}
	for _, n := range nodes {
		if n.Spec.Role == "ControlPlane" && n.InternalIP() != "" {
			return n.InternalIP()
		}
	}
	for _, n := range nodes {
		if n.InternalIP() != "" {
			return n.InternalIP()
		}
	}
	return ""
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	return host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "0.0.0.0" || host == "::"
}

func (c *JobController) submitterService(j *job.Job, name string) *service.Service {
	return &service.Service{
		TypeMeta: pod.TypeMeta{Kind: "Service", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{
			Name:      name,
			Namespace: j.Namespace,
			Labels:    submitterLabels(j),
		},
		Spec: service.ServiceSpec{
			Type:     service.ServiceTypeClusterIP,
			Selector: pod.LabelSelector{MatchLabels: submitterLabels(j)},
			Ports: []service.ServicePort{{
				Name:       "http",
				Protocol:   "TCP",
				Port:       8080,
				TargetPort: 8080,
			}},
		},
	}
}

func submitterName(jobName string) string {
	return "job-" + strings.ToLower(jobName) + "-submitter"
}

func submitterLabels(j *job.Job) map[string]string {
	return map[string]string{
		job.OwnerLabel: j.Name,
		"app":          submitterName(j.Name),
		"accelerator":  "gpu",
	}
}
