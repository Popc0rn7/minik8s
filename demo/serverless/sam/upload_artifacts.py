import argparse
import json
import mimetypes
import urllib.parse
import urllib.request
from pathlib import Path


def put_object(base_url, key, data, content_type):
    url = base_url.rstrip("/") + "/objects/" + urllib.parse.quote(key)
    request = urllib.request.Request(url, data=data, method="PUT", headers={"Content-Type": content_type})
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.loads(response.read().decode())


def artifact_ref(key):
    return "artifact://" + key.strip("/")


def upload_dataset(base_url, dataset_path, bucket):
    root = dataset_path.parent
    dataset = json.loads(dataset_path.read_text())
    dataset_key = f"{bucket}/dataset.json"
    put_object(base_url, dataset_key, json.dumps(dataset, ensure_ascii=False).encode(), "application/json")
    requests = []
    for case in dataset.get("cases", []):
        image_path = root / case["image"]["path"]
        image_key = f"{bucket}/images/{image_path.name}"
        content_type = mimetypes.guess_type(image_path.name)[0] or "application/octet-stream"
        put_object(base_url, image_key, image_path.read_bytes(), content_type)
        case["imageRef"] = artifact_ref(image_key)
        case["maskRef"] = artifact_ref(f"{bucket}/masks/{case['id']}.json")
        case["scoreRef"] = artifact_ref(f"{bucket}/scores/{case['id']}.json")
        requests.append({
            "caseId": case["id"],
            "imageId": case.get("image", {}).get("id", case["id"]),
            "imageRef": case["imageRef"],
            "image": {"id": case.get("image", {}).get("id", case["id"]), "type": "artifact", "artifactRef": case["imageRef"]},
            "prompt": case["prompt"],
            "target": case.get("target"),
            "maskRef": case["maskRef"],
            "scoreRef": case["scoreRef"],
        })
    put_object(base_url, dataset_key, json.dumps(dataset, ensure_ascii=False).encode(), "application/json")
    return artifact_ref(dataset_key), requests


def main():
    parser = argparse.ArgumentParser(description="Upload Most-Dog demo artifacts to the Minik8s artifact store.")
    parser.add_argument("--artifact-store", default="http://127.0.0.1:8080")
    parser.add_argument("--dataset", default="demo/serverless/sam/dataset.json")
    parser.add_argument("--bucket", default="most-dog")
    parser.add_argument("--output-dir", default="/tmp/most-dog-workflow-requests")
    args = parser.parse_args()

    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)
    dataset_ref, requests = upload_dataset(args.artifact_store, Path(args.dataset), args.bucket)
    for request in requests:
        (output_dir / f"{request['caseId']}.json").write_text(json.dumps(request, ensure_ascii=False) + "\n")
    ranking_request = {
        "datasetRef": dataset_ref,
        "rankingRef": artifact_ref(f"{args.bucket}/outputs/most-dog-ranking.json"),
    }
    (output_dir / "make-ranking.json").write_text(json.dumps(ranking_request, ensure_ascii=False) + "\n")
    print(json.dumps({"status": "ok", "datasetRef": dataset_ref, "requests": len(requests), "outputDir": str(output_dir)}, ensure_ascii=False))


if __name__ == "__main__":
    main()
