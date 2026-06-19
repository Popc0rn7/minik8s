import json
import pathlib
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, str(pathlib.Path(__file__).parent))

from app import attach_request_metadata, create_app, scale_prompt
import make_request
import make_collage


class FakeSegmenter:
    def segment(self, request):
        self.assert_request(request)
        return {
            "status": "ok",
            "model": "fake-sam",
            "imageSize": [320, 240],
            "promptType": "point",
            "mask": {
                "encoding": "rle",
                "counts": "fake-rle",
                "size": [240, 320],
                "area": 42,
                "bbox": [10, 20, 30, 40],
                "score": 0.99,
            },
        }

    def assert_request(self, request):
        if request["prompt"]["type"] != "point":
            raise AssertionError("expected point prompt")


class SamAppTest(unittest.TestCase):
    def test_invoke_returns_segmentation_json(self):
        app = create_app(FakeSegmenter())
        client = app.test_client()

        response = client.post(
            "/invoke",
            data=json.dumps(
                {
                    "image": {"type": "url", "value": "http://example.test/cat.jpg"},
                    "prompt": {"type": "point", "points": [[120, 80]], "labels": [1]},
                    "output": {"format": "rle"},
                }
            ),
        )

        self.assertEqual(200, response.status_code)
        payload = json.loads(response.data.decode())
        self.assertEqual("ok", payload["status"])
        self.assertEqual("point", payload["promptType"])
        self.assertEqual([10, 20, 30, 40], payload["mask"]["bbox"])

    def test_invoke_rejects_invalid_prompt(self):
        app = create_app(FakeSegmenter())
        client = app.test_client()

        response = client.post(
            "/invoke",
            data=json.dumps(
                {
                    "image": {"type": "url", "value": "http://example.test/cat.jpg"},
                    "prompt": {"type": "point", "points": [], "labels": []},
                }
            ),
        )

        self.assertEqual(400, response.status_code)
        payload = json.loads(response.data.decode())
        self.assertEqual("error", payload["status"])
        self.assertIn("points", payload["error"])

    def test_healthz_reports_ready(self):
        app = create_app(FakeSegmenter())
        client = app.test_client()

        response = client.get("/healthz")

        self.assertEqual(200, response.status_code)
        self.assertEqual("ok", response.data.decode())

    def test_scale_prompt_matches_resized_image(self):
        point_prompt = {"type": "point", "points": [[100, 50], [20, 10]], "labels": [1, 0]}
        box_prompt = {"type": "box", "box": [10, 20, 30, 40]}

        self.assertEqual(
            {"type": "point", "points": [[50.0, 25.0], [10.0, 5.0]], "labels": [1, 0]},
            scale_prompt(point_prompt, 0.5),
        )
        self.assertEqual(
            {"type": "box", "box": [5.0, 10.0, 15.0, 20.0]},
            scale_prompt(box_prompt, 0.5),
        )

    def test_attach_request_metadata_to_result(self):
        request = {
            "caseId": "truck-point-1",
            "image": {"id": "truck", "type": "base64", "value": "abc"},
            "target": {"kind": "dog", "contest": "most-dog"},
            "prompt": {"id": "truck-cab", "type": "point", "points": [[10, 20]], "labels": [1]},
            "expectedMask": {"path": "masks/truck-cab.json"},
        }
        result = {"status": "ok", "mask": {"area": 42}}

        attach_request_metadata(result, request)

        self.assertEqual("truck-point-1", result["caseId"])
        self.assertEqual("truck", result["imageId"])
        self.assertEqual({"kind": "dog", "contest": "most-dog"}, result["target"])
        self.assertEqual("truck-cab", result["promptId"])
        self.assertEqual("masks/truck-cab.json", result["expectedMask"]["path"])


class SamRequestBuilderTest(unittest.TestCase):
    def test_build_request_from_dataset_case(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            (root / "images").mkdir()
            (root / "masks").mkdir()
            (root / "images" / "truck.jpg").write_bytes(b"fake-image")
            (root / "masks" / "truck-cab.json").write_text("{}\n")
            dataset_path = root / "dataset.json"
            dataset_path.write_text(
                json.dumps(
                    {
                        "version": 1,
                        "cases": [
                            {
                                "id": "truck-point-1",
                                "image": {"id": "truck", "path": "images/truck.jpg"},
                                "target": {"kind": "dog", "contest": "most-dog"},
                                "prompt": {
                                    "id": "truck-cab",
                                    "type": "point",
                                    "points": [[500, 375]],
                                    "labels": [1],
                                },
                                "expectedMask": {"path": "masks/truck-cab.json"},
                            }
                        ],
                    }
                )
            )

            payload = make_request.build_request_from_dataset(dataset_path, "truck-point-1")

        self.assertEqual("truck-point-1", payload["caseId"])
        self.assertEqual({"id": "truck", "type": "base64", "value": "ZmFrZS1pbWFnZQ=="}, payload["image"])
        self.assertEqual({"kind": "dog", "contest": "most-dog"}, payload["target"])
        self.assertEqual("truck-cab", payload["prompt"]["id"])
        self.assertEqual("masks/truck-cab.json", payload["expectedMask"]["path"])

    def test_main_accepts_dataset_case(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            (root / "images").mkdir()
            (root / "images" / "cat.jpg").write_bytes(b"cat")
            dataset_path = root / "dataset.json"
            dataset_path.write_text(
                json.dumps(
                    {
                        "version": 1,
                        "cases": [
                            {
                                "id": "cat-box",
                                "image": {"id": "cat", "path": "images/cat.jpg"},
                                "prompt": {"id": "cat-body", "type": "box", "box": [1, 2, 3, 4]},
                            }
                        ],
                    }
                )
            )

            with mock.patch("sys.argv", ["make_request.py", "--dataset", str(dataset_path), "--case-id", "cat-box"]):
                with mock.patch("builtins.print") as fake_print:
                    make_request.main()

        payload = json.loads(fake_print.call_args.args[0])
        self.assertEqual("cat-box", payload["caseId"])
        self.assertEqual("Y2F0", payload["image"]["value"])

    def test_write_all_requests_from_dataset(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            output_dir = root / "requests"
            (root / "images").mkdir()
            (root / "images" / "a.jpg").write_bytes(b"a")
            (root / "images" / "b.jpg").write_bytes(b"b")
            dataset_path = root / "dataset.json"
            dataset_path.write_text(
                json.dumps(
                    {
                        "version": 1,
                        "cases": [
                            {
                                "id": "case-a",
                                "image": {"id": "a", "path": "images/a.jpg"},
                                "prompt": {"id": "a-point", "type": "point", "points": [[1, 2]], "labels": [1]},
                            },
                            {
                                "id": "case-b",
                                "image": {"id": "b", "path": "images/b.jpg"},
                                "prompt": {"id": "b-box", "type": "box", "box": [1, 2, 3, 4]},
                            },
                        ],
                    }
                )
            )

            paths = make_request.write_all_requests_from_dataset(dataset_path, output_dir)

            self.assertEqual([output_dir / "case-a.json", output_dir / "case-b.json"], paths)
            self.assertEqual("YQ==", json.loads((output_dir / "case-a.json").read_text())["image"]["value"])
            self.assertEqual("Yg==", json.loads((output_dir / "case-b.json").read_text())["image"]["value"])


class MostDogRankingTest(unittest.TestCase):
    def test_score_prefers_confident_centered_visible_dog(self):
        image_size = [1000, 800]
        centered = {
            "caseId": "centered",
            "mask": {"area": 240000, "bbox": [260, 160, 480, 480], "score": 0.95},
        }
        clipped = {
            "caseId": "clipped",
            "mask": {"area": 260000, "bbox": [0, 20, 520, 500], "score": 0.98},
        }

        centered_score = make_collage.most_dog_score(centered, image_size)
        clipped_score = make_collage.most_dog_score(clipped, image_size)

        self.assertGreater(centered_score["dogScore"], clipped_score["dogScore"])
        self.assertEqual("centered", centered_score["caseId"])

    def test_rank_results_orders_by_most_dog_score(self):
        dataset = {
            "cases": [
                {"id": "dog-a", "image": {"path": "images/a.jpg"}},
                {"id": "dog-b", "image": {"path": "images/b.jpg"}},
            ]
        }
        results = [
            {"caseId": "dog-a", "imageSize": [1000, 800], "mask": {"area": 100000, "bbox": [10, 10, 200, 200], "score": 0.80}},
            {"caseId": "dog-b", "imageSize": [1000, 800], "mask": {"area": 240000, "bbox": [260, 160, 480, 480], "score": 0.95}},
        ]

        ranking = make_collage.rank_results(dataset, results)

        self.assertEqual(["dog-b", "dog-a"], [item["caseId"] for item in ranking])
        self.assertEqual([1, 2], [item["rank"] for item in ranking])


if __name__ == "__main__":
    unittest.main()
