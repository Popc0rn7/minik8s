# Minik8s Acceptance README

本文是最终验收入口说明。课程功能规格以 `docs/Handout.md` 为准，最终提交与脚本要求以`docs/FINAL.md` 为准；本文只记录助教运行脚本前需要知道的环境假设、入口和人工确认项。

## Submission

- Repository: https://github.com/Popc0rn7/minik8s
- Final tag: v0.1.0
- Final commit: TODO
- Install root on target machines: `/opt/minik8s`。

现有交付布局固定为：

```text
/opt/minik8s/
├── bin/
│   ├── minik8s
│   └── kubectl
├── scripts/
│   └── acceptance/
├── manifests/
├── demo/
│   └── serverless/
│       └── harbor-incident-triage/
├── state/ # 运行时状态数据
├── static-pods/ # 启动时的静态Pod
├── dns/
└── secrets/
    └── gpu-ssh/

/etc/cni/ # CNI安装
└── net.d/

/opt/cni/ # CNI安装
└── bin/
```

## 00 Environment Requirements

### 提供环境

本项目配置好了三台符合要求的云主机，可以凭借专用ssh key在交大校园网下访问，详情见 `secrets/node-ssh`。

```bash
# node-a
ssh root@10.119.16.213 -i secrets/node-ssh/id_ed25519_minik8s
# node-b
ssh root@10.119.5.94 -i secrets/node-ssh/id_ed25519_minik8s
# node-c
ssh root@10.119.6.252 -i secrets/node-ssh/id_ed25519_minik8s
```

固定验收内网 IP：

| Node | 内网 IP |
| --- | --- |
| node-a | `192.168.1.4` |
| node-b | `192.168.1.10` |
| node-c | `192.168.1.15` |

直接检查环境：

```bash
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/00_env_check.sh
```

### 个人环境

`scripts/acceptance/00_env_check.sh` 会检查 OS、kernel、Go、Docker、必要命令、目录、端口和基础连通性。以下条件仍需在运行前确认：

- 要求至少有三台环境一致的机器来构建多节点环境，而且三台机器通过能够通过内网或其他网络通信。
- 除了互联之外，也要保证安全组开放端口
  - `TCP 18080`
  - `UDP 4789`
- 保持网络环境干净，确保没有 CNI、iptables 管理程序，或端口占用: `153,80,2379,2380,4222,8080,8088,18080,30080`
- Docker 镜像依赖DockerHub/GHCR，建议提前load在三台机器本地，避免运行时网络问题，所用镜像见：`docs/acceptance/images.md`。
- CNI 和 kube-proxy 数据面需要 root 权限，要求三台机器上都用root用户操作，或手动sudo操作。
- Slurm平台需要身份验证，需要提前准备好密钥和证书，这里提供`secrets/gpu-ssh`。

然后请在`scripts/acceptance/env.sh`中修改为对应的网络环境并运行：

```bash
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/00_env_check.sh
```

脚本日志遵循 `docs/FINAL.md` 的验收格式：每条检查输出 `[RUN]`、`[EXIT]`、`[OUTPUT]` 和对应结论 `[PASS]`/`[FAIL]`/`[LIMITED]`，最后输出 `[CLEANUP]` 和 `[END]`。`00_env_check.sh` 只做环境预检，不执行 `minik8s init`、不 `kubectl apply` CNI、不运行 `go test`，也不创建或删除集群资源。

## 01 Deploy

`scripts/acceptance/01_node_multinode.sh` 是多机启动脚本，支持同一个入口显式启动 bridge 或 sailer 节点。`00_env_check.sh` 已负责软件、端口、systemd 和 `/opt/minik8s` 布局预检；`01_node_multinode.sh` 只负责本机 service 生命周期和必要的 sailer join。

推荐运行顺序如下。三台机器均从 `/opt/minik8s` 执行：

```bash
# node-a：启动 bridge
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/01_node_multinode.sh bridge

# node-a：独立启动本机 worker sailer
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/01_node_multinode.sh sailer node-a

# node-b：仅作为 worker 启动 sailer
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/01_node_multinode.sh sailer node-b

# node-c：仅作为 worker 启动 sailer
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/01_node_multinode.sh sailer node-c

# 验证集群状态
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/01_node_multinode.sh
```

无参数模式在 node-a 上运行，用于展示/检验 `docs/FINAL.md` 7.1 的 Node 要求：
先检查本机 `sailer.json` 中的 join 身份，再检查 node-a 同时运行
`minik8s-bridge.service` 与 `minik8s-sailer.service`，并通过
`kubectl get nodes` / `kubectl describe node` 验证 node-a、node-b、node-c
三台 Node 已注册且 Ready。

## CICD

TODO

## Software Testing

TODO

## AI Usage

TODO

## Develop Process

TODO
