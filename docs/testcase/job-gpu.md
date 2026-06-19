# Job GPU Slurm Acceptance

本文验证 Minik8s `Job` 资源的 GPU/Slurm 后端。当前实现不是原生 GPU device
plugin，也不会把交我算 Slurm 节点加入 Minik8s 集群；它实现的是一个接近 Kubernetes
Job 的一次性任务抽象：每个 `Job` 创建独立 submitter Pod/Service，由 submitter 通过
SSH/SCP 把 CUDA 程序上传到交我算平台，使用 Slurm 编译运行并回收结果。

## 当前能力边界

已实现：

- `kind: Job`，`apiVersion: batch/v1`。
- 仅支持 `spec.selector.matchLabels.accelerator: gpu`。
- `spec.source.files` 会在 `kubectl apply -f` 时按 manifest 所在目录读取并随 Job 对象上传。
- `spec.slurm` 字段生成 `job.slurm`，默认队列为 `debuga100`，不会生成
  `#SBATCH --nodelist`。
- 控制面为每个 Job 创建独立 submitter Pod 和 Service。
- submitter 执行 `ssh/scp/sbatch/squeue/sacct`，并将 `.out/.err` 回写到
  `Job.status`。
- `kubectl get/describe/logs/delete job` 可查看状态、结果和删除任务。

需要真机环境补齐：

- submitter Pod 内可用 SSH 凭据。
- worker 节点能拉取 `ghcr.io/popc0rn7/gpu-submitter:v0.1.0`。
- submitter 能访问 Harbor API endpoint。
- 交我算账号有 `debuga100` 或 `dgx2` 等目标队列权限。

## 前置条件

node-a 和 node-b 使用 `docs/testcase/README.md` 的默认启动流程。额外确认：

```fish
set -gx HARBOR http://$NODE_A_IP:18080
set -gx MINIK8S_HARBOR $HARBOR
./bin/kubectl get nodes
./bin/kubectl get pods -n minik8s-system; or true
```

期望：

- 至少一个 worker 为 `Ready`。
- worker 可以拉取普通镜像。
- `curl --noproxy '*' -fsS $HARBOR/api-resources` 能访问 Harbor。

## GPU-00：准备 submitter 镜像

如果使用 GHCR 已发布镜像：

```fish
docker pull ghcr.io/popc0rn7/gpu-submitter:v0.1.0
```

如果需要从当前分支构建并推送：

```fish
docker login ghcr.io
make gpu-submitter-image
make push-gpu-submitter-image
docker pull ghcr.io/popc0rn7/gpu-submitter:v0.1.0
```

期望：

- worker 节点 `docker images` 能看到 `ghcr.io/popc0rn7/gpu-submitter:v0.1.0`。
- 如果 GHCR 镜像不可访问，应先改 `JobControllerConfig.SubmitterImage` 或后续 manifest
  注入机制，不要临时改 CUDA Job YAML。

## GPU-01：准备 SSH 凭据

当前实现不会从 YAML 读取密码，也不应在 YAML 中写明文密码。真机验证推荐使用 SSH key。
submitter 镜像内已包含 `openssh-client`，但当前 Pod spec 还没有 Secret/volume 注入能力。
因此可选路径如下：

1. 推荐后续补能力：实现 Secret/volume，把私钥挂载到 submitter Pod 的
   `/root/.ssh/id_rsa`，并挂载 `known_hosts`。
2. 临时真机验证：在 worker 上预置 Docker/容器运行时可访问的 SSH 配置，或进入 submitter
   容器手工放置 key 后继续验证。

最小 SSH 连通性检查：

```fish
ssh stu1718@sylogin.hpc.sjtu.edu.cn 'hostname && which sbatch && which squeue && which sacct'
```

期望：

- 能免交互登录。
- 输出能找到 `sbatch`、`squeue`、`sacct`。

## GPU-02：提交 CUDA vector add Job

```fish
./bin/kubectl apply -f manifests/job/cuda-add.yaml
./bin/kubectl get jobs
./bin/kubectl describe job cuda-add
./bin/kubectl get pods
./bin/kubectl get services
```

期望：

- `apply` 输出 `job/cuda-add created (...)`。
- `get jobs` 显示：

```text
JOB                            PHASE          ACCELERATOR    PARTITION      SLURM-JOB-ID
[job]  cuda-add                PodCreating    gpu            debuga100
```

- `describe job cuda-add` 显示 submitter Pod/Service 名称：

```text
Submitter Pod: job-cuda-add-submitter
Submitter Service: job-cuda-add-submitter
Remote Host: sylogin.hpc.sjtu.edu.cn
```

- `get pods` 有 `job-cuda-add-submitter`。
- `get services` 有 `job-cuda-add-submitter`。

## GPU-03：观察 Slurm 状态

当 submitter 成功提交后：

```fish
./bin/kubectl get jobs
./bin/kubectl describe job cuda-add
```

期望：

- `PHASE` 依次进入 `Submitted`、`Running`、`Collecting`，最终为
  `Succeeded` 或 `Failed`。
- `describe` 中出现：

```text
Slurm Job ID: <number>
Remote Dir: /dssg/home/acct-stu/stu1718/minik8s-gpujobs/cuda-add-...
Message: ...
```

在交我算登录节点上可交叉验证：

```fish
ssh stu1718@sylogin.hpc.sjtu.edu.cn 'squeue -j <SLURM_JOB_ID> || sacct -j <SLURM_JOB_ID> --format=JobID,State,ExitCode'
```

期望：

- 活跃任务能在 `squeue` 看到。
- 结束任务能在 `sacct` 看到 `COMPLETED` 或失败原因。

## GPU-04：查看 CUDA 结果

```fish
./bin/kubectl logs job cuda-add
```

期望输出包含：

```text
N = 1048576
threadsPerBlock = 256
blocksPerGrid = 4096
Result: PASS
```

答辩讲解点：

- `vector_add.cu` 中每个 CUDA thread 计算一个数组元素。
- `threadsPerBlock = 256`。
- `blocksPerGrid = (N + threadsPerBlock - 1) / threadsPerBlock`。
- `N = 1 << 20` 时并发处理 1,048,576 个元素。

真机记录：

- 2026-06-15 在 node-a `192.168.1.8` 上验证通过。
- `cuda-add` 最终状态为 `Succeeded`，Slurm Job ID 为 `58995816`。
- 远程目录为
  `/dssg/home/acct-stu/stu1718/minik8s-gpujobs/cuda-add-20260615070016`。
- `sacct -j 58995816 --format=JobID,State,ExitCode -P -n` 显示主 Job
  `COMPLETED|0:0`。
- `kubectl logs job cuda-add` 显示 A100 GPU、CUDA 12.2 `nvcc`，并输出
  `Result: PASS`。

## GPU-05：隔离性

```fish
./bin/kubectl apply -f manifests/job/cuda-add-2.yaml
./bin/kubectl get jobs
./bin/kubectl describe job cuda-add
./bin/kubectl describe job cuda-add-2
```

期望：

- 两个 Job 各自拥有独立 submitter Pod：
  - `job-cuda-add-submitter`
  - `job-cuda-add-2-submitter`
- 两个 Job 各自拥有独立 submitter Service。
- 两个 Job 的 `Remote Dir` 不同。
- 两个 Job 的 `Slurm Job ID` 不同。

## GPU-06：复杂 CUDA tiled matmul

该用例用于展示比 vector add 更充分的 CUDA 并发能力：二维 grid/block、shared memory
tile、block 内同步，以及结果回收。

```fish
./bin/kubectl apply -f manifests/job/cuda-matmul.yaml
./bin/kubectl get jobs
./bin/kubectl describe job cuda-matmul-tiled
./bin/kubectl logs job cuda-matmul-tiled
```

期望：

- `cuda-matmul-tiled` 进入 `Succeeded`。
- `describe` 显示独立 submitter Pod/Service：

```text
Submitter Pod: job-cuda-matmul-tiled-submitter
Submitter Service: job-cuda-matmul-tiled-submitter
```

- `logs` 至少包含：

```text
Matrix N = 1024
Tile size = 16
Block = 16 x 16
Grid = 64 x 64
Kernel: tiled shared-memory matrix multiplication
Result: PASS
```

答辩讲解点：

- 每个 CUDA thread 计算输出矩阵 `C` 的一个元素。
- 每个 block 覆盖 `16 x 16` 个输出元素。
- `__shared__` 的 `tileA` 和 `tileB` 缓存全局内存中的子矩阵 tile，减少重复读取。
- 每一轮 tile 载入后使用 `__syncthreads()` 保证 block 内所有线程都能看到完整 tile。
- `grid = 64 x 64`，`block = 16 x 16`，总共调度 1,048,576 个输出元素计算。

真机记录：

- 2026-06-15 在 node-a `192.168.1.8` 上验证通过。
- `cuda-matmul-tiled` 最终状态为 `Succeeded`，Slurm Job ID 为 `58996280`。
- 远程目录为
  `/dssg/home/acct-stu/stu1718/minik8s-gpujobs/cuda-matmul-tiled-20260615071456`。
- `sacct -j 58996280 --format=JobID,State,ExitCode -P -n` 显示主 Job
  `COMPLETED|0:0`。
- `kubectl logs job cuda-matmul-tiled` 显示 A100 GPU、CUDA 12.2 `nvcc`，并输出
  `Matrix N = 1024`、`Grid = 64 x 64`、`Kernel time ms = 27.3418`、
  `Max error = 0`、`Result: PASS`。

## GPU-07：删除和清理

```fish
./bin/kubectl delete job cuda-add
./bin/kubectl delete job cuda-add-2
./bin/kubectl delete job cuda-matmul-tiled
./bin/kubectl get jobs
./bin/kubectl get pods
./bin/kubectl get services
```

期望：

- Job 对象被删除。
- 对应 submitter Pod/Service 被删除。
- 如果 Job 已经提交 Slurm，控制面会 best-effort 执行 `scancel <jobid>`；在 HPC 上可用
  `squeue`/`sacct` 交叉确认。

## 故障定位

- `ImagePullBackOff` 或 Pod 创建失败：确认 worker 能拉取
  `ghcr.io/popc0rn7/gpu-submitter:v0.1.0`，或先执行 `make gpu-submitter-image` 和
  `make push-gpu-submitter-image`。
- submitter 无法访问 Harbor：当前 controller 给 submitter 的默认 Harbor 参数是
  `http://127.0.0.1:18080`，这只适合同节点/host 网络假设。多机真机环境需要后续补显式
  endpoint 注入，把 submitter 使用的 Harbor URL 配置为 node-a LAN 地址。
- SSH 失败：不要把密码写入 Job YAML。应使用 SSH key、known_hosts 和后续 Secret/volume
  注入能力。
- `sbatch` 失败：检查 `spec.slurm.partition`、`qos`、`gres` 和账号队列权限。默认使用
  `debuga100`，不要手写 `--nodelist`。
- `logs job` 为空：先看 `describe job` 的 `Message`、`Slurm Job ID` 和 `Remote Dir`，
  再登录 HPC 查看 `<jobid>.out` 和 `<jobid>.err` 是否生成。
