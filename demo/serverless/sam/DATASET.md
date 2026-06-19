# Most-Dog Dataset Format

Put local demo images under:

```text
demo/serverless/sam/images/
```

Put expected mask JSON files under:

```text
demo/serverless/sam/masks/
```

`dataset.json` is the case index. The demo task is "最狗的狗排名": SAM segments
each dog first, then a one-shot mounted Pod ranks the cut-out dogs and writes a
collage image back to the mounted workspace.

Each case describes one Function invocation:

```json
{
  "version": 1,
  "task": {
    "id": "most-dog-ranking",
    "title": "最狗的狗排名"
  },
  "cases": [
    {
      "id": "truck-point-1",
      "image": {
        "id": "truck",
        "path": "images/truck.jpg"
      },
      "target": {
        "kind": "dog",
        "contest": "most-dog"
      },
      "prompt": {
        "id": "truck-cab-point",
        "type": "point",
        "points": [[500, 375]],
        "labels": [1]
      },
      "expectedMask": {
        "path": "masks/truck-point-1.json"
      }
    },
    {
      "id": "truck-box-1",
      "image": {
        "id": "truck",
        "path": "images/truck.jpg"
      },
      "prompt": {
        "id": "truck-box",
        "type": "box",
        "box": [425, 600, 700, 875]
      },
      "expectedMask": {
        "path": "masks/truck-box-1.json"
      }
    }
  ]
}
```

Mask files may stay empty during preparation:

```json
{}
```

When you want to store expected results, use this shape:

```json
{
  "version": 1,
  "caseId": "truck-point-1",
  "imageId": "truck",
  "promptId": "truck-cab-point",
  "mask": {
    "encoding": "rle",
    "counts": "",
    "size": [800, 1200],
    "area": 0,
    "bbox": [0, 0, 0, 0],
    "score": null
  }
}
```

Generate masks after you choose boxes:

```bash
python3 demo/serverless/sam/generate_masks.py \
  --image demo/serverless/sam/images/01.jpg \
  --box '120,80,420,360' \
  --output demo/serverless/sam/masks/01.json \
  --case-id 01 \
  --image-id 01 \
  --prompt-id 01-box
```

Or generate every `prompt.type=box` case from `dataset.json`:

```bash
python3 demo/serverless/sam/generate_masks.py \
  --dataset demo/serverless/sam/dataset.json
```

Build an invoke request from a case:

```bash
python3 demo/serverless/sam/make_request.py \
  --dataset demo/serverless/sam/dataset.json \
  --case-id truck-point-1 \
  > /tmp/truck-point-1.json
```

Build invoke requests for every case:

```bash
python3 demo/serverless/sam/make_request.py \
  --dataset demo/serverless/sam/dataset.json \
  --all \
  --output-dir /tmp/sam-requests
```

Prepare the mounted workspace used by the collage Pod:

```bash
rm -rf /tmp/most-dog
mkdir -p /tmp/most-dog/images /tmp/most-dog/results
cp demo/serverless/sam/dataset.json /tmp/most-dog/dataset.json
cp demo/serverless/sam/images/* /tmp/most-dog/images/
cp demo/serverless/sam/make_collage.py /tmp/most-dog/make_collage.py
python3 demo/serverless/sam/make_request.py \
  --dataset /tmp/most-dog/dataset.json \
  --all \
  --output-dir /tmp/most-dog/requests
```

The generated request embeds the image as base64 and preserves `caseId`,
`image.id`, `prompt.id` and `expectedMask.path` so the Function response can be
matched back to the local expected mask file.

After invoking `sam-segment`, save each response as:

```text
/tmp/most-dog/results/<case-id>.json
```

Then run a restored one-shot collage Pod. It reads
`/tmp/most-dog/dataset.json`, `/tmp/most-dog/images/` and
`/tmp/most-dog/results/`, then writes:

```text
/tmp/most-dog/most-dog-ranking.png
/tmp/most-dog/most-dog-ranking.json
/tmp/most-dog/cutouts/*.png
```
