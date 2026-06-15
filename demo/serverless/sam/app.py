import base64
import json
import os
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

from image_workflow import ArtifactClient


def create_app(segmenter):
    return TestableApp(segmenter)


class TestableApp:
    def __init__(self, segmenter):
        self.segmenter = segmenter

    def test_client(self):
        return TestClient(self)


class TestClient:
    def __init__(self, app):
        self.app = app

    def post(self, path, data):
        status, body = handle_request(self.app.segmenter, path, data)
        return TestResponse(status, body)

    def get(self, path):
        status, body = handle_request(self.app.segmenter, path, "", method="GET")
        return TestResponse(status, body)


class TestResponse:
    def __init__(self, status_code, body):
        self.status_code = status_code
        self.data = body.encode()


def handle_request(segmenter, path, body, method="POST"):
    if method == "GET" and path == "/healthz":
        return 200, "ok"
    if method != "POST" or path != "/invoke":
        return 404, json.dumps({"status": "error", "error": "not found"})
    try:
        request = json.loads(body or "{}")
        validate_request(request)
        result = segmenter.segment(request)
        return 200, json.dumps(result)
    except ValueError as exc:
        return 400, json.dumps({"status": "error", "error": str(exc)})
    except Exception as exc:
        return 500, json.dumps({"status": "error", "error": str(exc)})


def validate_request(request):
    image = request.get("image")
    if not isinstance(image, dict):
        raise ValueError("image is required")
    image_type = image.get("type")
    if image_type not in {"url", "base64", "file", "artifact"}:
        raise ValueError("image.type must be url, base64, file, or artifact")
    if not image.get("value") and not image.get("artifactRef"):
        raise ValueError("image.value or image.artifactRef is required")
    prompt = request.get("prompt")
    if not isinstance(prompt, dict):
        raise ValueError("prompt is required")
    prompt_type = prompt.get("type")
    if prompt_type == "point":
        points = prompt.get("points")
        labels = prompt.get("labels")
        if not isinstance(points, list) or len(points) == 0:
            raise ValueError("prompt.points is required")
        if not isinstance(labels, list) or len(labels) != len(points):
            raise ValueError("prompt.labels must match prompt.points")
    elif prompt_type == "box":
        box = prompt.get("box")
        if not isinstance(box, list) or len(box) != 4:
            raise ValueError("prompt.box must contain four numbers")
    else:
        raise ValueError("prompt.type must be point or box")


def scale_prompt(prompt, scale):
    if scale == 1.0:
        return prompt
    if prompt["type"] == "point":
        return {
            "type": "point",
            "points": [[float(x) * scale, float(y) * scale] for x, y in prompt["points"]],
            "labels": prompt["labels"],
        }
    return {
        "type": "box",
        "box": [float(value) * scale for value in prompt["box"]],
    }


def attach_request_metadata(result, request):
    if request.get("caseId"):
        result["caseId"] = request["caseId"]
    image = request.get("image", {})
    if image.get("id"):
        result["imageId"] = image["id"]
    target = request.get("target")
    if isinstance(target, dict):
        result["target"] = target
    prompt = request.get("prompt", {})
    if prompt.get("id"):
        result["promptId"] = prompt["id"]
    expected_mask = request.get("expectedMask")
    if isinstance(expected_mask, dict) and expected_mask.get("path"):
        result["expectedMask"] = {"path": expected_mask["path"]}
    if request.get("imageRef"):
        result["imageRef"] = request["imageRef"]
    if request.get("maskRef"):
        result["maskRef"] = request["maskRef"]
    if request.get("scoreRef"):
        result["scoreRef"] = request["scoreRef"]
    return result


class SamSegmenter:
    def __init__(self):
        import cv2
        import numpy as np
        from pycocotools import mask as mask_utils
        from segment_anything import SamPredictor, sam_model_registry
        import torch

        checkpoint = os.environ.get("SAM_CHECKPOINT", "/models/sam_vit_b_01ec64.pth")
        model_type = os.environ.get("SAM_MODEL_TYPE", "vit_b")
        device = os.environ.get("SAM_DEVICE", "cpu")
        sam = sam_model_registry[model_type](checkpoint=checkpoint)
        sam.to(device=device)
        sam.eval()
        if device == "cpu":
            torch.set_num_threads(int(os.environ.get("SAM_CPU_THREADS", "2")))
        self.predictor = SamPredictor(sam)
        self.cv2 = cv2
        self.np = np
        self.mask_utils = mask_utils
        self.model_name = "sam-" + model_type

    def segment(self, request):
        image, scale = self._load_image(request["image"])
        self.predictor.set_image(image)
        prompt = scale_prompt(request["prompt"], scale)
        if prompt["type"] == "point":
            masks, scores, _ = self.predictor.predict(
                point_coords=self.np.array(prompt["points"]),
                point_labels=self.np.array(prompt["labels"]),
                multimask_output=True,
            )
        else:
            masks, scores, _ = self.predictor.predict(
                box=self.np.array(prompt["box"]),
                multimask_output=True,
            )
        best = int(self.np.argmax(scores))
        mask = masks[best].astype("uint8")
        rle = self.mask_utils.encode(self.np.asfortranarray(mask))
        rle["counts"] = rle["counts"].decode("ascii")
        bbox = [int(v) for v in self.mask_utils.toBbox(rle).tolist()]
        result = {
            "status": "ok",
            "model": self.model_name,
            "imageSize": [int(image.shape[1]), int(image.shape[0])],
            "promptType": prompt["type"],
            "mask": {
                "encoding": "rle",
                "counts": rle["counts"],
                "size": [int(v) for v in rle["size"]],
                "area": int(self.mask_utils.area(rle)),
                "bbox": bbox,
                "score": float(scores[best]),
            },
        }
        result = attach_request_metadata(result, request)
        mask_ref = request.get("maskRef")
        if mask_ref:
            ArtifactClient().put_json(mask_ref, result)
        return result

    def _load_image(self, image_ref):
        image_type = image_ref["type"]
        value = image_ref.get("value")
        if image_type == "url":
            with urllib.request.urlopen(value, timeout=10) as response:
                data = response.read()
        elif image_type == "base64":
            data = base64.b64decode(value)
        elif image_type == "artifact":
            data, _ = ArtifactClient().get_bytes(image_ref.get("artifactRef") or value)
        else:
            with open(value, "rb") as f:
                data = f.read()
        array = self.np.frombuffer(data, dtype=self.np.uint8)
        image = self.cv2.imdecode(array, self.cv2.IMREAD_COLOR)
        if image is None:
            raise ValueError("image could not be decoded")
        image = self.cv2.cvtColor(image, self.cv2.COLOR_BGR2RGB)
        max_side = int(os.environ.get("SAM_MAX_IMAGE_SIDE", "1024"))
        height, width = image.shape[:2]
        scale = min(1.0, max_side / max(height, width))
        if scale < 1.0:
            image = self.cv2.resize(image, (int(width * scale), int(height * scale)))
        return image, scale


class ServerHandler(BaseHTTPRequestHandler):
    segmenter = None

    def do_GET(self):
        status, body = handle_request(self.segmenter, self.path, "", method="GET")
        self._write(status, body)

    def do_POST(self):
        size = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(size).decode()
        status, response = handle_request(self.segmenter, self.path, body)
        self._write(status, response)

    def _write(self, status, body):
        payload = body.encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json" if body != "ok" else "text/plain")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, format: str, *args: Any) -> None:
        return


def main():
    port = int(os.environ.get("SAM_PORT", os.environ.get("MINIK8S_FUNCTION_PORT", "8080")))
    ServerHandler.segmenter = SamSegmenter()
    ThreadingHTTPServer(("0.0.0.0", port), ServerHandler).serve_forever()


if __name__ == "__main__":
    main()
