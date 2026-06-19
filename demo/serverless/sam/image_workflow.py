import json
import mimetypes
import os
import tempfile
import time
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import make_collage


DEFAULT_ARTIFACT_STORE_URL = "http://10.96.0.1:8080"


def json_response(status, payload):
    return status, "application/json", json.dumps(payload, ensure_ascii=False).encode()


def text_response(status, payload):
    return status, "text/plain", payload.encode()


def parse_json(body):
    if not body:
        return {}
    return json.loads(body.decode())


def artifact_key(ref):
    if not isinstance(ref, str) or not ref.startswith("artifact://"):
        raise ValueError(f"artifact ref must start with artifact://, got {ref!r}")
    key = ref[len("artifact://") :]
    if not key or key.startswith("/") or ".." in Path(key).parts:
        raise ValueError(f"invalid artifact ref {ref!r}")
    return key


def artifact_ref(key):
    return "artifact://" + key.strip("/")


class ArtifactClient:
    def __init__(self, base_url=None):
        self.base_url = (base_url or os.environ.get("ARTIFACT_STORE_URL") or DEFAULT_ARTIFACT_STORE_URL).rstrip("/")

    def get_bytes(self, ref):
        key = urllib.parse.quote(artifact_key(ref))
        with urllib.request.urlopen(f"{self.base_url}/objects/{key}", timeout=30) as response:
            return response.read(), response.headers.get("Content-Type", "application/octet-stream")

    def get_json(self, ref):
        data, _ = self.get_bytes(ref)
        return json.loads(data.decode())

    def put_bytes(self, ref, data, content_type="application/octet-stream"):
        key = urllib.parse.quote(artifact_key(ref))
        request = urllib.request.Request(
            f"{self.base_url}/objects/{key}",
            data=data,
            method="PUT",
            headers={"Content-Type": content_type},
        )
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.loads(response.read().decode())

    def put_json(self, ref, payload):
        return self.put_bytes(ref, json.dumps(payload, ensure_ascii=False).encode(), "application/json")


class ArtifactStore:
    def __init__(self, root=None):
        self.root = Path(root or os.environ.get("ARTIFACT_STORE_ROOT", "/data")).resolve()
        self.root.mkdir(parents=True, exist_ok=True)

    def path_for(self, raw_key):
        key = urllib.parse.unquote(raw_key)
        path = (self.root / key).resolve()
        if self.root not in path.parents and path != self.root:
            raise ValueError("artifact key escapes store root")
        return path

    def handle(self, method, path, body, headers):
        if method == "GET" and path == "/healthz":
            return text_response(200, "ok")
        if path.startswith("/objects/"):
            raw_key = path[len("/objects/") :]
            return self.handle_object(method, raw_key, body, headers)
        if method == "GET" and path.startswith("/metadata/"):
            raw_key = path[len("/metadata/") :]
            return self.handle_metadata(raw_key)
        return json_response(404, {"status": "error", "error": "not found"})

    def handle_object(self, method, raw_key, body, headers):
        path = self.path_for(raw_key)
        meta_path = path.with_name(path.name + ".meta.json")
        if method == "PUT":
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(body)
            content_type = headers.get("Content-Type") or mimetypes.guess_type(path.name)[0] or "application/octet-stream"
            metadata = {
                "status": "ok",
                "key": urllib.parse.unquote(raw_key),
                "size": len(body),
                "contentType": content_type,
                "updatedAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            }
            meta_path.write_text(json.dumps(metadata, ensure_ascii=False) + "\n")
            return json_response(200, metadata)
        if method == "GET":
            if not path.exists() or not path.is_file():
                return json_response(404, {"status": "error", "error": "artifact not found"})
            content_type = mimetypes.guess_type(path.name)[0] or "application/octet-stream"
            if meta_path.exists():
                try:
                    content_type = json.loads(meta_path.read_text()).get("contentType") or content_type
                except json.JSONDecodeError:
                    pass
            return 200, content_type, path.read_bytes()
        return json_response(405, {"status": "error", "error": "method not allowed"})

    def handle_metadata(self, raw_key):
        path = self.path_for(raw_key)
        meta_path = path.with_name(path.name + ".meta.json")
        if meta_path.exists():
            return json_response(200, json.loads(meta_path.read_text()))
        if not path.exists() or not path.is_file():
            return json_response(404, {"status": "error", "error": "artifact not found"})
        return json_response(200, {
            "status": "ok",
            "key": urllib.parse.unquote(raw_key),
            "size": path.stat().st_size,
            "contentType": mimetypes.guess_type(path.name)[0] or "application/octet-stream",
        })


def extract_metadata(event):
    client = ArtifactClient()
    image_ref = event["imageRef"]
    data, content_type = client.get_bytes(image_ref)
    output = dict(event)
    output.update({
        "status": "ok",
        "step": "extract-metadata",
        "image": {
            "id": event.get("imageId", event.get("caseId")),
            "type": "artifact",
            "artifactRef": image_ref,
            "contentType": content_type,
            "sizeBytes": len(data),
        },
        "maskRef": event.get("maskRef") or artifact_ref(f"most-dog/masks/{event['caseId']}.json"),
        "scoreRef": event.get("scoreRef") or artifact_ref(f"most-dog/scores/{event['caseId']}.json"),
    })
    return output


def score_mask(event):
    client = ArtifactClient()
    result = client.get_json(event["maskRef"])
    image_size = result.get("imageSize") or event.get("imageSize")
    if not image_size:
        raise ValueError("imageSize is required")
    score = make_collage.most_dog_score(result, image_size)
    score.update({
        "status": "ok",
        "step": "score-mask",
        "maskRef": event["maskRef"],
        "scoreRef": event.get("scoreRef") or artifact_ref(f"most-dog/scores/{result['caseId']}.json"),
        "imageRef": event.get("imageRef"),
    })
    client.put_json(score["scoreRef"], score)
    return score


def make_ranking(event):
    client = ArtifactClient()
    dataset = client.get_json(event["datasetRef"])
    results = [client.get_json(ref) for ref in event.get("maskRefs", [])]
    if not results:
        for case in dataset.get("cases", []):
            ref = case.get("maskRef") or artifact_ref(f"most-dog/masks/{case['id']}.json")
            results.append(client.get_json(ref))
    ranking = make_collage.rank_results(dataset, results)
    collage_ref = event.get("collageRef") or artifact_ref("most-dog/outputs/most-dog-ranking.png")
    with tempfile.TemporaryDirectory(prefix="minik8s-collage-") as tmp:
        workspace = Path(tmp)
        images_dir = workspace / "images"
        results_dir = workspace / "results"
        images_dir.mkdir()
        results_dir.mkdir()
        for case in dataset.get("cases", []):
            image_ref = case.get("imageRef") or case.get("image", {}).get("artifactRef")
            if not image_ref:
                image_ref = artifact_ref(f"most-dog/images/{Path(case['image']['path']).name}")
            data, _ = client.get_bytes(image_ref)
            image_path = workspace / case["image"]["path"]
            image_path.parent.mkdir(parents=True, exist_ok=True)
            image_path.write_bytes(data)
        for result in results:
            (results_dir / f"{result['caseId']}.json").write_text(json.dumps(result, ensure_ascii=False) + "\n")
        output_png = workspace / "most-dog-ranking.png"
        make_collage.render_collage(workspace, dataset, results, ranking, output_png)
        client.put_bytes(collage_ref, output_png.read_bytes(), "image/png")
    report = {
        "status": "ok",
        "step": "make-collage",
        "task": dataset.get("task", {"id": "most-dog-ranking", "title": "最狗的狗排名"}),
        "ranking": ranking,
    }
    ranking_ref = event.get("rankingRef") or artifact_ref("most-dog/outputs/most-dog-ranking.json")
    report["output"] = {"imageRef": collage_ref, "jsonRef": ranking_ref}
    client.put_json(ranking_ref, report)
    return {**report, "rankingRef": ranking_ref, "collageRef": collage_ref}


class RuntimeHandler(BaseHTTPRequestHandler):
    artifact_store = None
    role = os.environ.get("MINIK8S_IMAGE_WORKFLOW_ROLE", "artifact-store")

    def do_GET(self):
        self.dispatch()

    def do_PUT(self):
        self.dispatch()

    def do_POST(self):
        self.dispatch()

    def dispatch(self):
        size = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(size)
        try:
            if self.role == "artifact-store":
                if self.__class__.artifact_store is None:
                    self.__class__.artifact_store = ArtifactStore()
                store = self.__class__.artifact_store
                status, content_type, payload = store.handle(self.command, self.path, body, self.headers)
            else:
                status, content_type, payload = self.invoke(body)
        except Exception as exc:
            status, content_type, payload = json_response(500, {"status": "error", "error": str(exc)})
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def invoke(self, body):
        if self.command == "GET" and self.path == "/healthz":
            return text_response(200, "ok")
        if self.command != "POST" or self.path != "/invoke":
            return json_response(404, {"status": "error", "error": "not found"})
        event = parse_json(body)
        if self.role == "extract-metadata":
            return json_response(200, extract_metadata(event))
        if self.role == "score-mask":
            return json_response(200, score_mask(event))
        if self.role == "make-collage":
            return json_response(200, make_ranking(event))
        return json_response(500, {"status": "error", "error": f"unknown role {self.role}"})

    def log_message(self, format, *args):
        return


def main():
    port = int(os.environ.get("MINIK8S_FUNCTION_PORT", os.environ.get("PORT", "8080")))
    ThreadingHTTPServer(("0.0.0.0", port), RuntimeHandler).serve_forever()


if __name__ == "__main__":
    main()
