# Most-Dog SAM Serverless Demo

This demo packages Meta Segment Anything Model (SAM) as a Minik8s Serverless
container Function and uses a mounted one-shot Pod to produce "最狗的狗排名".
SAM returns masks for each dog image; the Pod cuts out the dogs, ranks them with
a transparent scoring rule and writes a collage back to the mounted workspace.

The preferred Serverless presentation is the workflow path below. It keeps
Functions stateless: large images and generated artifacts live in an
`artifact-store`, which is a small hostPath-backed stand-in for CouchDB, S3 or
MinIO. Workflow steps pass only JSON metadata and `artifact://...` references.

```text
                         Serverless Image Workflow

Client / Event
     |
     |  HTTP invoke / NATS event
     v
+-------------------+
| process-one-image |
| Workflow          |
+-------------------+
     |
     v
+------------------+        read imageRef         +----------------------+
| extract-metadata | ---------------------------> | Artifact Store       |
| Function         |                              | CouchDB/S3-like      |
+------------------+ <--------------------------- | hostPath-backed demo |
     | metadata JSON                              +----------------------+
     | caseId, imageRef, prompt,
     | maskRef, scoreRef
     v
+------------------+        read imageRef
| sam-segment      | ---------------------------> +----------------------+
| Function         |                              | Artifact Store       |
| SAM model        |        write maskRef         | images / masks       |
+------------------+ ---------------------------> +----------------------+
     |
     | mask metadata JSON
     | caseId, imageSize, maskRef
     v
+------------------+        read maskRef
| score-mask       | ---------------------------> +----------------------+
| Function         |                              | Artifact Store       |
+------------------+        write scoreRef        | scores               |
     |             -----------------------------> +----------------------+
     |
     | score JSON
     | dogScore, bbox, scoreRef
     v
+-----------------------------+
| process-one-image completes |
+-----------------------------+


Batch mode:

Client submits N images concurrently
     |
     +--> process-one-image(case 01)
     +--> process-one-image(case 02)
     +--> process-one-image(case 03)
     +--> ...
              |
              v
      fn-sam-segment scales
          0 -> 1 -> N


Final merge:

Client
  |
  | invoke
  v
+--------------+
| make-ranking |
| Workflow     |
+--------------+
  |
  v
+--------------+        read datasetRef, maskRefs, imageRefs
| make-collage | ------------------------------------------+
| Function     |                                           |
+--------------+                                           v
  |                                             +----------------------+
  | write rankingRef, collageRef                | Artifact Store       |
  +-------------------------------------------> | dataset / masks      |
                                                | ranking / collage    |
                                                +----------------------+
  |
  v
+-----------------------+
| return final metadata |
| rankingRef            |
| collageRef            |
+-----------------------+
```

Use `docs/testcase/serverless-image-workflow.md` for the full workflow demo.
The older base64 invoke plus one-shot collage Pod path is still documented
below as a simpler fallback.

## Image

Build the CPU image:

```bash
docker build -t minik8s/sam-cpu:demo demo/serverless/sam
```

The image downloads the SAM ViT-B checkpoint at build time and stores it at:

```text
/models/sam_vit_b_01ec64.pth
```

The same image also contains `/app/make_collage.py`, so the final collage Pod
does not require a second image.

For the current demo manifest, the Pod runs `/workspace/make_collage.py` from
the hostPath mount. That means an already-built SAM image can still run the
collage task without rebuilding the 2GB image; just copy the script into
`/tmp/most-dog`.

For a two-node demo, make sure both worker nodes can use the image. One simple
offline path is:

```bash
docker save minik8s/sam-cpu:demo -o /tmp/sam-cpu-demo.tar
scp /tmp/sam-cpu-demo.tar node-1:/tmp/
scp /tmp/sam-cpu-demo.tar node-2:/tmp/
ssh node-1 'docker load -i /tmp/sam-cpu-demo.tar'
ssh node-2 'docker load -i /tmp/sam-cpu-demo.tar'
```

## Function

This SAM demo is legacy material and is not part of the final acceptance
manifests. To revive it, create a Function manifest for the built image first.

Apply the Function if you have restored that manifest:

```bash
./kubectl apply -f <sam-function.yaml>
./kubectl get functions
./kubectl describe function sam-segment
```

The Function uses:

```yaml
runtime: container
image: minik8s/sam-cpu
imageTag: demo
port: 8080
targetConcurrency: 1
idleTimeoutSeconds: 120
```

## Invoke

The recommended demo path is to send the image as base64 in the invoke request.
This keeps the worker Pod independent from external image URLs and avoids baking
demo images into the SAM image.

For a prepared demo set, put files here:

```text
demo/serverless/sam/images/        # jpg/png source images
demo/serverless/sam/masks/         # expected mask JSON, can be {} at first
demo/serverless/sam/dataset.json   # case index
```

The dataset format is documented in `demo/serverless/sam/DATASET.md`.

Prepare the host workspace for the one-shot collage Pod:

```bash
rm -rf /tmp/most-dog
mkdir -p /tmp/most-dog/images /tmp/most-dog/results
cp demo/serverless/sam/dataset.json /tmp/most-dog/dataset.json
cp demo/serverless/sam/images/* /tmp/most-dog/images/
cp demo/serverless/sam/make_collage.py /tmp/most-dog/make_collage.py
```

Build a request from a dataset case:

```bash
python3 demo/serverless/sam/make_request.py \
  --dataset /tmp/most-dog/dataset.json \
  --case-id 01 \
  > /tmp/01.json
```

Build requests for all dataset cases:

```bash
python3 demo/serverless/sam/make_request.py \
  --dataset /tmp/most-dog/dataset.json \
  --all \
  --output-dir /tmp/most-dog/requests
```

After selecting boxes, generate expected mask JSON locally:

```bash
python3 demo/serverless/sam/generate_masks.py \
  --image demo/serverless/sam/images/01.jpg \
  --box '120,80,420,360' \
  --output demo/serverless/sam/masks/01.json \
  --case-id 01 \
  --image-id 01 \
  --prompt-id 01-box
```

Build a point prompt request from a local image:

```bash
python3 demo/serverless/sam/make_request.py \
  --image /path/to/image.jpg \
  --point '500,375:1' \
  > /tmp/sam-point.json
```

Build a box prompt request:

```bash
python3 demo/serverless/sam/make_request.py \
  --image /path/to/image.jpg \
  --box '425,600,700,875' \
  > /tmp/sam-box.json
```

Invoke with base64 request JSON:

```bash
./minik8s invoke function sam-segment --data "$(cat /tmp/01.json)"
```

For the full ranking demo, save each SAM response under:

```text
/tmp/most-dog/results/<case-id>.json
```

Then run a one-shot collage Pod if you have restored that manifest:

```bash
./kubectl delete pod most-dog-collage -n demo; true
./kubectl apply -f <most-dog-collage-pod.yaml>
```

It writes:

```text
/tmp/most-dog/most-dog-ranking.png
/tmp/most-dog/most-dog-ranking.json
/tmp/most-dog/cutouts/*.png
```

The repository also keeps URL-based sample requests for environments where the
Function Pod can access the public image URL.

URL point prompt:

```bash
./minik8s invoke function sam-segment \
  --data "$(tr -d '\n' < demo/serverless/sam/sample_request_point.json)"
```

URL box prompt:

```bash
./minik8s invoke function sam-segment \
  --data "$(tr -d '\n' < demo/serverless/sam/sample_request_box.json)"
```

The response is JSON with mask metadata:

```json
{
  "status": "ok",
  "model": "sam-vit_b",
  "imageSize": [1200, 800],
  "promptType": "point",
  "caseId": "01",
  "imageId": "01",
  "promptId": "01-box",
  "target": {"kind": "dog", "contest": "most-dog"},
  "expectedMask": {"path": "masks/01.json"},
  "mask": {
    "encoding": "rle",
    "counts": "...",
    "size": [800, 1200],
    "area": 12345,
    "bbox": [10, 20, 300, 200],
    "score": 0.98
  }
}
```

## Runtime Contract

The container listens on `0.0.0.0:8080` and exposes:

- `GET /healthz`: returns `ok`.
- `POST /invoke`: accepts a JSON string request body and returns a JSON string
  response body.

Supported image sources:

- `{"image":{"type":"base64","value":"..."}}`
- `{"image":{"type":"url","value":"https://..."}}`
- `{"image":{"type":"file","value":"/path/in/container.jpg"}}`

Supported prompts:

- point prompt: `{"type":"point","points":[[x,y]],"labels":[1]}`
- box prompt: `{"type":"box","box":[x1,y1,x2,y2]}`

If `SAM_MAX_IMAGE_SIDE` resizes the input image, the runtime scales point and
box coordinates by the same ratio before calling SAM.

## Notes

- CPU SAM inference is slow. Use `vit_b`, small demo images and
  `targetConcurrency: 1`.
- The image is large because it contains PyTorch and the SAM checkpoint.
- The demo assumes worker nodes already have network access during image build
  or receive the built image through `docker save/load`.
- The old collage Pod manifest pinned the hostPath workspace to `node-a` via
  `nodeSelector`. If `/tmp/most-dog` lives on another worker, use the matching
  selector before applying a restored Pod manifest.
