import argparse
import base64
import json
from pathlib import Path


def parse_point(value):
    points = []
    labels = []
    for item in value.split(";"):
        item = item.strip()
        if not item:
            continue
        coord, label = item.split(":")
        x, y = coord.split(",")
        points.append([float(x), float(y)])
        labels.append(int(label))
    if not points:
        raise ValueError("at least one point is required")
    return {"type": "point", "points": points, "labels": labels}


def parse_box(value):
    parts = [float(part.strip()) for part in value.split(",")]
    if len(parts) != 4:
        raise ValueError("box must be x1,y1,x2,y2")
    return {"type": "box", "box": parts}


def encode_image(path):
    return base64.b64encode(path.read_bytes()).decode("ascii")


def build_request(image_path, prompt):
    return {
        "image": {
            "type": "base64",
            "value": encode_image(image_path),
        },
        "prompt": prompt,
    }


def build_request_from_dataset(dataset_path, case_id):
    dataset_path = Path(dataset_path)
    dataset = json.loads(dataset_path.read_text())
    for case in dataset.get("cases", []):
        if case.get("id") != case_id:
            continue
        image = case["image"]
        image_path = dataset_path.parent / image["path"]
        payload = {
            "caseId": case["id"],
            "image": {
                "id": image.get("id"),
                "type": "base64",
                "value": encode_image(image_path),
            },
            "prompt": case["prompt"],
        }
        if case.get("target"):
            payload["target"] = case["target"]
        expected_mask = case.get("expectedMask")
        if expected_mask:
            payload["expectedMask"] = expected_mask
        return payload
    raise ValueError(f"case not found: {case_id}")


def dataset_case_ids(dataset_path):
    dataset = json.loads(Path(dataset_path).read_text())
    return [case["id"] for case in dataset.get("cases", [])]


def write_all_requests_from_dataset(dataset_path, output_dir):
    dataset_path = Path(dataset_path)
    output_dir = Path(output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)
    written = []
    for case_id in dataset_case_ids(dataset_path):
        payload = build_request_from_dataset(dataset_path, case_id)
        path = output_dir / f"{case_id}.json"
        path.write_text(json.dumps(payload, separators=(",", ":")) + "\n")
        written.append(path)
    return written


def main():
    parser = argparse.ArgumentParser(description="Build a base64 SAM invoke request.")
    parser.add_argument("--image", help="local image path")
    parser.add_argument("--dataset", help="dataset JSON path")
    parser.add_argument("--case-id", help="case id from dataset JSON")
    parser.add_argument("--all", action="store_true", help="write every dataset case to --output-dir")
    parser.add_argument("--output-dir", help="directory for --all generated request JSON files")
    group = parser.add_mutually_exclusive_group()
    group.add_argument("--point", help="point prompt, for example '500,375:1;420,320:0'")
    group.add_argument("--box", help="box prompt, for example '425,600,700,875'")
    parser.add_argument("--pretty", action="store_true", help="print indented JSON")
    args = parser.parse_args()

    if args.dataset or args.case_id:
        if args.all:
            if not args.dataset or not args.output_dir:
                parser.error("--all requires --dataset and --output-dir")
            for path in write_all_requests_from_dataset(args.dataset, args.output_dir):
                print(path)
            return
        if not args.dataset or not args.case_id:
            parser.error("--dataset and --case-id must be used together")
        payload = build_request_from_dataset(args.dataset, args.case_id)
    else:
        if not args.image:
            parser.error("--image is required without --dataset")
        if not args.point and not args.box:
            parser.error("--point or --box is required without --dataset")
        payload = build_request(Path(args.image), parse_point(args.point) if args.point else parse_box(args.box))
    if args.pretty:
        print(json.dumps(payload, indent=2))
    else:
        print(json.dumps(payload, separators=(",", ":")))


if __name__ == "__main__":
    main()
