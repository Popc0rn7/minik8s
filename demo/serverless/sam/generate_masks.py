import argparse
import json
from pathlib import Path

from app import SamSegmenter


def parse_box(value):
    parts = [float(part.strip()) for part in value.split(",")]
    if len(parts) != 4:
        raise ValueError("box must be x1,y1,x2,y2")
    return parts


def mask_document(result, request):
    document = {
        "version": 1,
        "caseId": request.get("caseId"),
        "imageId": request.get("image", {}).get("id"),
        "promptId": request.get("prompt", {}).get("id"),
        "model": result["model"],
        "imageSize": result["imageSize"],
        "prompt": request["prompt"],
        "mask": result["mask"],
    }
    if request.get("target"):
        document["target"] = request["target"]
    return document


def segment_file(segmenter, image_path, box, output_path, case_id=None, image_id=None, prompt_id=None, target=None):
    request = {
        "caseId": case_id,
        "image": {
            "id": image_id,
            "type": "file",
            "value": str(image_path),
        },
        "prompt": {
            "id": prompt_id,
            "type": "box",
            "box": box,
        },
    }
    if target:
        request["target"] = target
    result = segmenter.segment(request)
    document = mask_document(result, request)
    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text(json.dumps(document, indent=2) + "\n")
    return output_path


def segment_dataset(segmenter, dataset_path):
    dataset_path = Path(dataset_path)
    dataset = json.loads(dataset_path.read_text())
    written = []
    for case in dataset.get("cases", []):
        prompt = case.get("prompt", {})
        if prompt.get("type") != "box":
            continue
        image = case["image"]
        expected_mask = case.get("expectedMask")
        if not expected_mask or not expected_mask.get("path"):
            raise ValueError(f"case {case.get('id')} is missing expectedMask.path")
        written.append(
            segment_file(
                segmenter,
                dataset_path.parent / image["path"],
                prompt["box"],
                dataset_path.parent / expected_mask["path"],
                case_id=case.get("id"),
                image_id=image.get("id"),
                prompt_id=prompt.get("id"),
                target=case.get("target"),
            )
        )
    return written


def main():
    parser = argparse.ArgumentParser(description="Generate expected SAM mask JSON from local images and box prompts.")
    parser.add_argument("--dataset", help="dataset JSON path; processes every box prompt case")
    parser.add_argument("--image", help="single local image path")
    parser.add_argument("--box", help="single box prompt: x1,y1,x2,y2")
    parser.add_argument("--output", help="single output mask JSON path")
    parser.add_argument("--case-id")
    parser.add_argument("--image-id")
    parser.add_argument("--prompt-id")
    args = parser.parse_args()

    segmenter = SamSegmenter()
    if args.dataset:
        for path in segment_dataset(segmenter, args.dataset):
            print(path)
        return
    if not args.image or not args.box or not args.output:
        parser.error("--image, --box and --output are required without --dataset")
    path = segment_file(
        segmenter,
        Path(args.image),
        parse_box(args.box),
        Path(args.output),
        case_id=args.case_id,
        image_id=args.image_id,
        prompt_id=args.prompt_id,
        target={"kind": "dog", "contest": "most-dog"} if args.case_id else None,
    )
    print(path)


if __name__ == "__main__":
    main()
