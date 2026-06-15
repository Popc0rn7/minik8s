# Serverless Image Workflow 测试用例

本文档验证一个更贴近 Handout 和参考图的图像处理 Serverless Workflow。函数之间只传
JSON metadata 和 `artifact://` 引用；图片、mask、score、collage 存在
`artifact-store` 中。这里的 `artifact-store` 是 CouchDB/S3/MinIO 风格对象存储的教学版
替代，底层用 hostPath 保存对象，不声明为生产级存储。

## 前置条件

- 已按 `docs/testcase/serverless.md` 的 SL-00 启动 serverless addon。
- 已重新构建并分发包含 `image_workflow.py` 的镜像：

```fish
docker build -t minik8s/sam-cpu:demo demo/serverless/sam
```

如果是双节点环境，两台节点都需要 `docker load` 这个镜像。

## 部署 Artifact Store

```fish
./kubectl delete service artifact-store; or true
./kubectl delete pod artifact-store; or true
./kubectl apply -f manifest/pod/pod_artifact_store.yaml
./kubectl apply -f manifest/service/service_artifact_store.yaml
sleep 5
./kubectl get pods
./kubectl get services
```

期望：

- `artifact-store` Pod Running。
- `artifact-store` Service 有 ClusterIP 和 endpoint。
- `artifact-store` 固定运行，不作为 Function 缩零。
- Function manifest 默认使用 `http://10.96.0.1:8080` 访问 Artifact Store。若
  `./kubectl get services` 显示 `artifact-store` 不是 `10.96.0.1`，先把
  `manifest/function/function_extract_metadata.yaml`、`function_sam_segment.yaml`、
  `function_score_mask.yaml`、`function_make_collage.yaml` 中的
  `ARTIFACT_STORE_URL` 替换为实际 ClusterIP。

## 上传图片和 dataset

如果宿主机可以直接访问 artifact-store，可使用 Service/NodePort/临时端口转发后的 HTTP
地址。以下以本机 `http://127.0.0.1:8080` 为例：

```fish
python3 demo/serverless/sam/upload_artifacts.py \
  --artifact-store http://127.0.0.1:8080 \
  --dataset demo/serverless/sam/dataset.json \
  --output-dir /tmp/most-dog-workflow-requests
```

期望输出包含 `datasetRef: artifact://most-dog/dataset.json`，并在
`/tmp/most-dog-workflow-requests/` 生成每张图的 workflow 请求 JSON。

## 部署 Functions 和 Workflows

```fish
./kubectl apply -f manifest/function/function_extract_metadata.yaml
./kubectl apply -f manifest/function/function_sam_segment.yaml
./kubectl apply -f manifest/function/function_score_mask.yaml
./kubectl apply -f manifest/function/function_make_collage.yaml
./kubectl apply -f manifest/function/workflow_process_one_image.yaml
./kubectl apply -f manifest/function/workflow_make_ranking.yaml
sleep 5
./kubectl get functions
./kubectl get workflows
./kubectl get replicasets
```

期望：

- 四个 Function 均存在。
- `process-one-image` 和 `make-ranking` Workflow 存在。
- 首次调用前 `fn-*` ReplicaSet 可保持 0 副本。

## 单图 Workflow

```fish
./minik8s invoke workflow process-one-image \
  --data "$(cat /tmp/most-dog-workflow-requests/01.json)"
./kubectl describe workflow process-one-image
./kubectl describe function sam-segment
```

期望：

- Workflow steps 依次为 `extract-metadata -> sam-segment -> score-mask`。
- 输出包含 `maskRef`、`scoreRef`、`dogScore`。
- Artifact Store 中出现 `most-dog/masks/01.json` 和 `most-dog/scores/01.json`。

## 批量与并发扩容

```fish
for request in /tmp/most-dog-workflow-requests/[0-9][0-9].json
  ./minik8s invoke workflow process-one-image --data "$(cat $request)" > /tmp/(basename $request .json).out 2>&1 &
end

for i in (seq 1 20)
  date "+sample %H:%M:%S"
  ./kubectl get replicasets | grep fn-sam-segment
  ./kubectl get pods | grep fn-sam-segment
  sleep 2
end
wait
```

期望：

- 所有请求成功返回。
- `fn-sam-segment` 的期望副本或实际 Pod 数曾大于 1，证明模型 Function 承接并发扩容。

## 合成 Ranking

```fish
./minik8s invoke workflow make-ranking \
  --data "$(cat /tmp/most-dog-workflow-requests/make-ranking.json)"
./kubectl describe workflow make-ranking
```

期望：

- 输出包含 `rankingRef` 和 `collageRef`。
- Artifact Store 中出现：
  - `artifact://most-dog/outputs/most-dog-ranking.json`
  - `artifact://most-dog/outputs/most-dog-ranking.png`
- Ranking 按 `dogScore` 降序。

## EventTrigger

```fish
./kubectl apply -f manifest/function/eventtrigger_image_uploaded.yaml
./minik8s publish minik8s.image.uploaded \
  --data "$(cat /tmp/most-dog-workflow-requests/01.json)"
./kubectl describe function extract-metadata
```

期望：

- EventTrigger 能收到 `minik8s.image.uploaded` 事件并触发入口 Function。
- 这里用于证明事件触发路径；完整链路仍以 Workflow invoke 展示。

## 清理

```fish
./kubectl delete eventtrigger image-uploaded; or true
./kubectl delete workflow process-one-image; or true
./kubectl delete workflow make-ranking; or true
./kubectl delete function extract-metadata; or true
./kubectl delete function sam-segment; or true
./kubectl delete function score-mask; or true
./kubectl delete function make-collage; or true
./kubectl delete service artifact-store; or true
./kubectl delete pod artifact-store; or true
```
