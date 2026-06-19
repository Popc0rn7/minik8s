import argparse
import json
from pathlib import Path

import matplotlib.pyplot as plt

from app import SamSegmenter
from generate_masks import segment_file


IMAGE_SUFFIXES = {".jpg", ".jpeg", ".png", ".bmp", ".webp"}


def image_id(path):
    return path.stem


def output_path_for(image_path, masks_dir):
    return masks_dir / f"{image_path.stem}.json"


def select_box(image_path):
    image = plt.imread(image_path)
    fig, ax = plt.subplots()
    ax.imshow(image)
    ax.set_title(f"{image_path.name}: click top-left and bottom-right")
    ax.axis("off")
    points = plt.ginput(2, timeout=0)
    plt.close(fig)
    if len(points) != 2:
        return None
    (x1, y1), (x2, y2) = points
    left, right = sorted([x1, x2])
    top, bottom = sorted([y1, y2])
    return [round(left, 2), round(top, 2), round(right, 2), round(bottom, 2)]


def dataset_case(case_id, image_path, mask_path, box):
    return {
        "id": case_id,
        "image": {
            "id": image_id(image_path),
            "path": str(image_path),
        },
        "target": {
            "kind": "dog",
            "contest": "most-dog",
        },
        "prompt": {
            "id": f"{case_id}-box",
            "type": "box",
            "box": box,
        },
        "expectedMask": {
            "path": str(mask_path),
        },
    }


def write_dataset(dataset_path, cases):
    dataset_path.write_text(
        json.dumps(
            {
                "version": 1,
                "task": {
                    "id": "most-dog-ranking",
                    "title": "最狗的狗排名",
                    "description": "Use Serverless SAM masks and a one-shot mounted Pod to rank the most poster-ready dog.",
                },
                "cases": cases,
            },
            indent=2,
            ensure_ascii=False,
        )
        + "\n"
    )


def main():
    parser = argparse.ArgumentParser(description="Click two box corners for each image and generate SAM mask JSON.")
    parser.add_argument("--images-dir", default="demo/serverless/sam/images")
    parser.add_argument("--masks-dir", default="demo/serverless/sam/masks")
    parser.add_argument("--dataset", default="demo/serverless/sam/dataset.json")
    parser.add_argument("--skip-existing", action="store_true")
    args = parser.parse_args()

    images_dir = Path(args.images_dir)
    masks_dir = Path(args.masks_dir)
    dataset_path = Path(args.dataset)
    masks_dir.mkdir(parents=True, exist_ok=True)

    images = sorted(path for path in images_dir.iterdir() if path.suffix.lower() in IMAGE_SUFFIXES)
    if not images:
        raise SystemExit(f"no images found in {images_dir}")

    segmenter = SamSegmenter()
    cases = []
    for image_path in images:
        mask_path = output_path_for(image_path, masks_dir)
        case_id = image_path.stem
        if args.skip_existing and mask_path.exists() and mask_path.read_text().strip() not in {"", "{}"}:
            print(f"skip existing {mask_path}")
            continue
        box = select_box(image_path)
        if box is None:
            print(f"skip {image_path}")
            continue
        print(f"{image_path}: box={box}")
        segment_file(
            segmenter,
            image_path,
            box,
            mask_path,
            case_id=case_id,
            image_id=image_id(image_path),
            prompt_id=f"{case_id}-box",
            target={"kind": "dog", "contest": "most-dog"},
        )
        cases.append(dataset_case(case_id, image_path.relative_to(dataset_path.parent), mask_path.relative_to(dataset_path.parent), box))
        print(f"wrote {mask_path}")

    if cases:
        write_dataset(dataset_path, cases)
        print(f"wrote {dataset_path}")


if __name__ == "__main__":
    main()
