import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
import uuid

spec = importlib.util.spec_from_file_location("helper", Path(__file__).with_name("helper.py"))
helper = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helper)

class StorageContractTest(unittest.TestCase):
    def configuration(self):
        return dict(schema_version=1, capacity_bytes=10 * 2**30, **{key: str(uuid.uuid4()) for key in ["asset_id", "instance_id", "org_id", "owner_id"]})

    def test_strict_identity_and_capacity(self):
        config = self.configuration()
        self.assertEqual(helper.parse(json.dumps(config)), config)
        for patch in [{"asset_id": "../elsewhere"}, {"schema_version": True}, {"capacity_bytes": 1}, {"capacity_bytes": True}, {"capacity_bytes": 1.5 * 2**30}, {"capacity_bytes": 101 * 2**30}, {"owner_id": None}, {"path": "/uncontrolled"}]:
            with self.assertRaises(ValueError):
                helper.parse(json.dumps({**config, **patch}))
        with self.assertRaises(ValueError):
            helper.parse(json.dumps(config)[:-1] + ',"schema_version":1}')

    def test_integer_gib_capacity_boundaries(self):
        for gib in [1, 10, 20, 100]:
            config = {**self.configuration(), "capacity_bytes": gib * 2**30}
            self.assertEqual(helper.parse(json.dumps(config)), config)

    def test_missing_original_volume_is_never_initialized(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            config = self.configuration()
            with self.assertRaises(FileNotFoundError):
                helper.execute("inspect", config, base)
            self.assertEqual(list(base.iterdir()), [])

    def test_preparation_never_formats_existing_assets(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            config = self.configuration()
            asset = base / config["asset_id"]
            asset.mkdir(mode=0o700)
            retained = asset / "retained-proof"
            retained.write_text("original-data")
            with self.assertRaises(FileExistsError):
                helper.execute("prepare", config, base)
            self.assertEqual(retained.read_text(), "original-data")

if __name__ == "__main__":
    unittest.main()
