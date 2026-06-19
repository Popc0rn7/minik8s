import json
import tempfile
import unittest
from pathlib import Path

import image_workflow


class ArtifactStoreTest(unittest.TestCase):
    def test_put_get_and_metadata(self):
        with tempfile.TemporaryDirectory() as tmp:
            store = image_workflow.ArtifactStore(tmp)

            status, _, body = store.handle("PUT", "/objects/most-dog/test.json", b'{"ok": true}', {"Content-Type": "application/json"})
            self.assertEqual(200, status)
            self.assertEqual("most-dog/test.json", json.loads(body.decode())["key"])

            status, content_type, body = store.handle("GET", "/objects/most-dog/test.json", b"", {})
            self.assertEqual(200, status)
            self.assertEqual("application/json", content_type)
            self.assertEqual({"ok": True}, json.loads(body.decode()))

            status, _, body = store.handle("GET", "/metadata/most-dog/test.json", b"", {})
            self.assertEqual(200, status)
            self.assertEqual(12, json.loads(body.decode())["size"])

    def test_rejects_path_escape(self):
        with tempfile.TemporaryDirectory() as tmp:
            store = image_workflow.ArtifactStore(tmp)

            with self.assertRaises(ValueError):
                store.path_for("../outside")


class WorkflowHelpersTest(unittest.TestCase):
    def test_extract_metadata_builds_artifact_payload(self):
        with tempfile.TemporaryDirectory() as tmp:
            store = image_workflow.ArtifactStore(tmp)
            store.handle("PUT", "/objects/most-dog/images/01.jpg", b"fake-image", {"Content-Type": "image/jpeg"})
            testcase = self

            class LocalClient(image_workflow.ArtifactClient):
                def get_bytes(self, ref):
                    status, content_type, body = store.handle("GET", "/objects/" + image_workflow.artifact_key(ref), b"", {})
                    testcase.assertEqual(200, status)
                    return body, content_type

            original = image_workflow.ArtifactClient
            image_workflow.ArtifactClient = LocalClient
            try:
                output = image_workflow.extract_metadata({
                    "caseId": "01",
                    "imageRef": "artifact://most-dog/images/01.jpg",
                    "prompt": {"type": "box", "box": [1, 2, 3, 4]},
                })
            finally:
                image_workflow.ArtifactClient = original

            self.assertEqual("artifact", output["image"]["type"])
            self.assertEqual("artifact://most-dog/images/01.jpg", output["image"]["artifactRef"])
            self.assertEqual("artifact://most-dog/masks/01.json", output["maskRef"])
            self.assertEqual("artifact://most-dog/scores/01.json", output["scoreRef"])

    def test_upload_dataset_request_shape(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "images").mkdir()
            (root / "images" / "01.jpg").write_bytes(b"fake-image")
            dataset_path = root / "dataset.json"
            dataset_path.write_text(json.dumps({
                "cases": [{
                    "id": "01",
                    "image": {"id": "01", "path": "images/01.jpg"},
                    "prompt": {"type": "box", "box": [1, 2, 3, 4]},
                }]
            }))
            captured = {}

            def fake_put(base_url, key, data, content_type):
                captured[key] = (data, content_type)
                return {"status": "ok"}

            import upload_artifacts

            original = upload_artifacts.put_object
            upload_artifacts.put_object = fake_put
            try:
                dataset_ref, requests = upload_artifacts.upload_dataset("http://store", dataset_path, "most-dog")
            finally:
                upload_artifacts.put_object = original

            self.assertEqual("artifact://most-dog/dataset.json", dataset_ref)
            self.assertEqual("artifact://most-dog/images/01.jpg", requests[0]["imageRef"])
            self.assertEqual("artifact", requests[0]["image"]["type"])
            self.assertIn("most-dog/images/01.jpg", captured)


if __name__ == "__main__":
    unittest.main()
