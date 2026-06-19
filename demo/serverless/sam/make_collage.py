import argparse
import json
import math
from pathlib import Path


def clamp(value, low=0.0, high=1.0):
    return max(low, min(high, value))


def most_dog_score(result, image_size):
    width, height = image_size
    mask = result["mask"]
    area = float(mask.get("area", 0))
    x, y, box_width, box_height = [float(v) for v in mask["bbox"]]
    image_area = max(1.0, float(width * height))
    area_ratio = area / image_area
    bbox_area = max(1.0, box_width * box_height)
    compactness = clamp(area / bbox_area)

    sam_score = clamp(float(mask.get("score", 0)))
    dog_area_score = 1.0 - clamp(abs(area_ratio - 0.32) / 0.32)

    dog_center_x = x + box_width / 2.0
    dog_center_y = y + box_height / 2.0
    image_center_x = width / 2.0
    image_center_y = height / 2.0
    distance = math.hypot(dog_center_x - image_center_x, dog_center_y - image_center_y)
    max_distance = max(1.0, math.hypot(width, height) / 2.0)
    centered_score = 1.0 - clamp(distance / max_distance)

    edge_margin = min(x, y, width - (x + box_width), height - (y + box_height))
    bbox_completeness = clamp(edge_margin / max(1.0, min(width, height) * 0.08))
    silhouette_score = 1.0 - clamp(abs(compactness - 0.55) / 0.55)

    dog_score = (
        0.35 * sam_score
        + 0.25 * dog_area_score
        + 0.20 * centered_score
        + 0.10 * bbox_completeness
        + 0.10 * silhouette_score
    )
    return {
        "caseId": result.get("caseId"),
        "imageId": result.get("imageId"),
        "promptId": result.get("promptId"),
        "dogScore": round(dog_score, 4),
        "samScore": round(sam_score, 4),
        "areaRatio": round(area_ratio, 4),
        "centeredScore": round(centered_score, 4),
        "bboxCompleteness": round(bbox_completeness, 4),
        "silhouetteScore": round(silhouette_score, 4),
        "bbox": [int(v) for v in mask["bbox"]],
    }


def rank_results(dataset, results):
    cases = {case["id"]: case for case in dataset.get("cases", [])}
    ranking = []
    for result in results:
        case_id = result["caseId"]
        image_size = result.get("imageSize")
        if not image_size:
            case = cases.get(case_id, {})
            image_size = case.get("imageSize")
        if not image_size:
            raise ValueError(f"missing imageSize for {case_id}")
        item = most_dog_score(result, image_size)
        case = cases.get(case_id, {})
        item["imagePath"] = case.get("image", {}).get("path")
        item["expectedMaskPath"] = case.get("expectedMask", {}).get("path")
        ranking.append(item)
    ranking.sort(key=lambda item: item["dogScore"], reverse=True)
    for index, item in enumerate(ranking, start=1):
        item["rank"] = index
    return ranking


def load_json(path):
    return json.loads(Path(path).read_text())


def load_results(results_dir):
    results = []
    for path in sorted(Path(results_dir).glob("*.json")):
        results.append(load_json(path))
    return results


def decode_mask(mask):
    from pycocotools import mask as mask_utils

    rle = {"counts": mask["counts"].encode("ascii"), "size": mask["size"]}
    return mask_utils.decode(rle)


def save_cutout(cv2, np, image, mask, bbox, path):
    x, y, width, height = [int(v) for v in bbox]
    x = max(0, x)
    y = max(0, y)
    width = max(1, min(width, image.shape[1] - x))
    height = max(1, min(height, image.shape[0] - y))
    crop = image[y : y + height, x : x + width]
    alpha = (mask[y : y + height, x : x + width] * 255).astype("uint8")
    rgba = cv2.cvtColor(crop, cv2.COLOR_BGR2BGRA)
    rgba[:, :, 3] = alpha
    path.parent.mkdir(parents=True, exist_ok=True)
    cv2.imwrite(str(path), rgba)
    return rgba


def composite_rgba(np, canvas, rgba, x, y, max_width, max_height):
    height, width = rgba.shape[:2]
    scale = min(max_width / max(1, width), max_height / max(1, height), 1.0)
    if scale != 1.0:
        import cv2

        rgba = cv2.resize(rgba, (int(width * scale), int(height * scale)), interpolation=cv2.INTER_AREA)
        height, width = rgba.shape[:2]
    alpha = rgba[:, :, 3:4].astype("float32") / 255.0
    rgb = rgba[:, :, :3].astype("float32")
    region = canvas[y : y + height, x : x + width].astype("float32")
    canvas[y : y + height, x : x + width] = (alpha * rgb + (1.0 - alpha) * region).astype("uint8")


def render_collage(workspace, dataset, results, ranking, output_png):
    import cv2
    import numpy as np

    workspace = Path(workspace)
    output_png = Path(output_png)
    output_png.parent.mkdir(parents=True, exist_ok=True)
    case_results = {result["caseId"]: result for result in results}
    cases = {case["id"]: case for case in dataset.get("cases", [])}

    columns = 5
    card_width = 300
    card_height = 420
    title_height = 120
    margin = 30
    rows = max(1, math.ceil(len(ranking) / columns))
    canvas_width = columns * card_width + margin * 2
    canvas_height = title_height + rows * card_height + margin
    canvas = np.full((canvas_height, canvas_width, 3), 246, dtype=np.uint8)

    cv2.putText(canvas, "Most-Dog Ranking", (margin, 62), cv2.FONT_HERSHEY_SIMPLEX, 1.6, (25, 25, 25), 3, cv2.LINE_AA)
    cv2.putText(canvas, "SAM masks + one-shot Pod collage", (margin, 98), cv2.FONT_HERSHEY_SIMPLEX, 0.8, (80, 80, 80), 2, cv2.LINE_AA)

    cutout_dir = workspace / "cutouts"
    for index, item in enumerate(ranking):
        row = index // columns
        col = index % columns
        card_x = margin + col * card_width
        card_y = title_height + row * card_height
        cv2.rectangle(canvas, (card_x + 8, card_y + 8), (card_x + card_width - 8, card_y + card_height - 8), (255, 255, 255), -1)
        cv2.rectangle(canvas, (card_x + 8, card_y + 8), (card_x + card_width - 8, card_y + card_height - 8), (210, 210, 210), 2)

        result = case_results[item["caseId"]]
        case = cases[item["caseId"]]
        image = cv2.imread(str(workspace / case["image"]["path"]), cv2.IMREAD_COLOR)
        if image is None:
            raise ValueError(f"image could not be read: {case['image']['path']}")
        mask = decode_mask(result["mask"])
        mask_height, mask_width = mask.shape[:2]
        if image.shape[0] != mask_height or image.shape[1] != mask_width:
            image = cv2.resize(image, (mask_width, mask_height), interpolation=cv2.INTER_AREA)
        cutout = save_cutout(cv2, np, image, mask, item["bbox"], cutout_dir / f"{item['caseId']}.png")
        composite_rgba(np, canvas, cutout, card_x + 35, card_y + 70, card_width - 70, card_height - 150)

        label = f"#{item['rank']} {item['caseId']}"
        score = f"most-dog={item['dogScore']:.2f}"
        cv2.putText(canvas, label, (card_x + 24, card_y + 42), cv2.FONT_HERSHEY_SIMPLEX, 0.75, (25, 25, 25), 2, cv2.LINE_AA)
        cv2.putText(canvas, score, (card_x + 24, card_y + card_height - 48), cv2.FONT_HERSHEY_SIMPLEX, 0.62, (40, 80, 160), 2, cv2.LINE_AA)
        cv2.putText(canvas, f"sam={item['samScore']:.2f} area={item['areaRatio']:.2f}", (card_x + 24, card_y + card_height - 22), cv2.FONT_HERSHEY_SIMPLEX, 0.5, (90, 90, 90), 1, cv2.LINE_AA)

    cv2.imwrite(str(output_png), canvas)


def run(workspace, dataset_path, results_dir, output_png, output_json):
    workspace = Path(workspace)
    dataset = load_json(dataset_path)
    results = load_results(results_dir)
    ranking = rank_results(dataset, results)
    render_collage(workspace, dataset, results, ranking, output_png)
    output = {
        "status": "ok",
        "task": dataset.get("task", {"id": "most-dog-ranking", "title": "最狗的狗排名"}),
        "ranking": ranking,
        "output": {"image": str(output_png), "json": str(output_json)},
    }
    Path(output_json).write_text(json.dumps(output, ensure_ascii=False, indent=2) + "\n")
    return output


def main():
    parser = argparse.ArgumentParser(description="Build the most-dog ranking collage from SAM results.")
    parser.add_argument("--workspace", default="/workspace")
    parser.add_argument("--dataset", default="/workspace/dataset.json")
    parser.add_argument("--results", default="/workspace/results")
    parser.add_argument("--output", default="/workspace/most-dog-ranking.png")
    parser.add_argument("--report", default="/workspace/most-dog-ranking.json")
    args = parser.parse_args()

    output = run(args.workspace, args.dataset, args.results, args.output, args.report)
    print(json.dumps(output, ensure_ascii=False))


if __name__ == "__main__":
    main()
