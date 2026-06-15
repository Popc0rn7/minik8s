# Serverless SAM Container Demo

本文档验证一个模型类 Serverless demo：最狗的狗排名。第一阶段用 Serverless SAM
Function 给 10 张狗图生成 mask；第二阶段用一个挂载 hostPath 的一次性 Pod 读取 mask
和原图，生成排名拼图后退出。它用于补足 `serverless.md` 中轻量文本函数之外的复杂应用
展示，但不替代 SL-03 并发扩容测试。

边界：本 demo 使用 CPU 跑 SAM ViT-B，首次构建和首次推理都较慢；镜像构建、模型权重下载
和节点镜像分发需要提前完成。Minik8s 负责运行预构建容器、冷启动、路由、scale-to-0，
以及用普通 Pod+hostPath 跑一次性拼图任务；当前不声明支持 Kubernetes Job 资源。

## 前置条件

- 已按 `docs/testcase/serverless.md` 的 SL-00 启动 serverless addon。
- node-a 和 node-b 均能运行 Docker。
- 构建机器可访问 PyTorch CPU wheel、GitHub 和 SAM checkpoint 下载地址。
- 控制面机器准备 10 张本地 jpg/png 狗图，放在 `demo/serverless/sam/images/`。

## 构建镜像

在仓库根目录：

```fish
docker build -t minik8s/sam-cpu:demo demo/serverless/sam
```

镜像会下载 SAM ViT-B checkpoint 到 `/models/sam_vit_b_01ec64.pth`，并包含
`/app/make_collage.py` 供后续一次性 Pod 使用。如果现场网络不稳定，建议提前构建并保存
镜像：

```fish
docker save minik8s/sam-cpu:demo -o /tmp/sam-cpu-demo.tar
```

## 分发到 worker

两台 worker 都需要能本地拉起 `minik8s/sam-cpu:demo`：

```fish
scp /tmp/sam-cpu-demo.tar node-a:/tmp/
scp /tmp/sam-cpu-demo.tar node-b:/tmp/
ssh node-a 'docker load -i /tmp/sam-cpu-demo.tar'
ssh node-b 'docker load -i /tmp/sam-cpu-demo.tar'
```

如果测试环境使用 `node-1/node-2` 命名，替换上述主机名即可。

## 部署 Function

```fish
./kubectl delete function sam-segment; or true
./kubectl apply -f manifest/function/function_sam_segment.yaml
sleep 5
./kubectl describe function sam-segment
./kubectl get replicasets
./kubectl get services
```

期望：

- `sam-segment` 显示 `runtime: container`。
- `Image` 为 `minik8s/sam-cpu:demo`。
- 出现 `fn-sam-segment` ReplicaSet 和 Service。
- 首次 invoke 前，ReplicaSet 可保持 0 副本。

## 生成 base64 请求

推荐把图片放在 invoke 请求体中，而不是让 Pod 访问外网图片 URL。这样 SAM 阶段只依赖
`minik8s/sam-cpu:demo` 镜像和本地请求文件。

如果要批量准备 demo case，把图片放在：

```text
demo/serverless/sam/images/
```

把对应的期望 mask JSON 放在：

```text
demo/serverless/sam/masks/
```

mask JSON 可以先写成空对象 `{}`。`demo/serverless/sam/dataset.json` 负责把 case、图片、
prompt、目标语义和 mask 文件路径关联起来，格式见 `demo/serverless/sam/DATASET.md`。

准备 hostPath workspace。`pod_most_dog_collage.yaml` 默认挂载 node-a 的
`/tmp/most-dog`，因此这些命令要在 node-a 执行；如果保存目录在 node-b，先改 manifest
里的 `nodeSelector`：

```fish
rm -rf /tmp/most-dog
mkdir -p /tmp/most-dog/images /tmp/most-dog/results
cp demo/serverless/sam/dataset.json /tmp/most-dog/dataset.json
cp demo/serverless/sam/images/* /tmp/most-dog/images/
cp demo/serverless/sam/make_collage.py /tmp/most-dog/make_collage.py
```

从 dataset case 生成请求：

```fish
python3 demo/serverless/sam/make_request.py \
  --dataset /tmp/most-dog/dataset.json \
  --case-id 01 \
  > /tmp/sam-point.json
```

从 dataset 一次生成所有请求，适合提前准备 10 个调用：

```fish
rm -rf /tmp/most-dog/requests
python3 demo/serverless/sam/make_request.py \
  --dataset /tmp/most-dog/dataset.json \
  --all \
  --output-dir /tmp/most-dog/requests
ls /tmp/most-dog/requests
```

点提示请求：

```fish
python3 demo/serverless/sam/make_request.py \
  --image /tmp/sam-demo.jpg \
  --point '500,375:1' \
  > /tmp/sam-point.json
```

框提示请求：

```fish
python3 demo/serverless/sam/make_request.py \
  --image /tmp/sam-demo.jpg \
  --box '425,600,700,875' \
  > /tmp/sam-box.json
```

坐标以原始图片像素为准；如果 runtime 内部缩小图片，会按相同比例缩放 prompt 坐标。

## 单 case 推理

```fish
./minik8s invoke function sam-segment --data "$(cat /tmp/most-dog/requests/01.json)" \
  > /tmp/most-dog/results/01.json
```

期望输出 JSON 包含：

- `status: ok`
- `model: sam-vit_b`
- `caseId`、`imageId`、`promptId`、`target` 和 `expectedMask.path`
- `promptType: box`
- `mask.encoding: rle`
- `mask.area` 大于 0
- `mask.bbox` 为四个整数

随后检查冷启动后端：

```fish
./kubectl get functions
./kubectl get replicasets
./kubectl get pods
```

期望 `fn-sam-segment` 已扩到 1，Function 的 `lastOutput` 或状态字段记录最近调用。

## 批量生成 10 个 mask

按实际 case 文件逐个调用，并保存结果到 `/tmp/most-dog/results/`：

```fish
for request in /tmp/most-dog/requests/*.json
    set case_id (basename $request .json)
    ./minik8s invoke function sam-segment --data "$(cat $request)" \
      > /tmp/most-dog/results/$case_id.json
end

ls /tmp/most-dog/results
```

期望每个 result 都包含非空 RLE mask。CPU 推理耗时和宿主机性能相关，必要时查看 bridge
和 `fn-sam-segment-*` Pod 日志确认仍在推理。

## 一次性 Pod 生成排名拼图

确认 `/tmp/most-dog` 在 node-a 上存在，然后创建拼图 Pod：

```fish
./kubectl delete pod most-dog-collage -n demo; or true
./kubectl apply -f manifest/pod/pod_most_dog_collage.yaml
sleep 10
./kubectl get pods -n demo
```

期望：

- `most-dog-collage` 被调度到 node-a。
- 容器运行 `/workspace/make_collage.py`，完成后退出。
- `/tmp/most-dog/most-dog-ranking.png` 被写回 hostPath。
- `/tmp/most-dog/most-dog-ranking.json` 包含 `ranking`，按 `dogScore` 降序排列。
- `/tmp/most-dog/cutouts/*.png` 包含抠出的狗。

如果 Pod 一直重启，确认 manifest 使用 `restartPolicy: Never`，并检查当前 sailer 对
已退出容器的状态展示；本 demo 只要求文件写回和容器完成，不声明 Kubernetes Job 语义。

## scale-to-0

SAM demo 的 manifest 将 `idleTimeoutSeconds` 设置为 120，避免模型加载后马上缩零：

```fish
sleep 140
./kubectl get replicasets
./kubectl get pods
```

期望 `fn-sam-segment` 回到 0 副本，后端 Pod 被清理。再次 invoke 应触发重新冷启动。

## 清理

```fish
./kubectl delete function sam-segment; or true
./kubectl delete pod most-dog-collage -n demo; or true
sleep 8
./kubectl get functions
./kubectl get replicasets
./kubectl get services
./kubectl get pods
```

期望 `sam-segment`、`fn-sam-segment` ReplicaSet、Service 和后端 Pod 均被删除。
