import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
import uuid

spec = importlib.util.spec_from_file_location("hc_inventory", Path(__file__).with_name("inventory.py"))
inventory = importlib.util.module_from_spec(spec)
spec.loader.exec_module(inventory)

class ReadOnlyInventoryTests(unittest.TestCase):
    def test_injected_manifest_symlink_is_never_followed(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            asset = base / str(uuid.uuid4())
            asset.mkdir(mode=0o700)
            secret = base / "private-secret"
            secret.write_text("this is never a manifest")
            secret.chmod(0o600)
            (asset / "manifest.json").symlink_to(secret)
            rows = inventory.census(base)
            self.assertEqual(len(rows), 2)
            self.assertTrue(all(not row["identity_valid"] for row in rows))
            self.assertNotIn("this is never", json.dumps(rows))
            self.assertTrue((asset / "manifest.json").is_symlink())

    def test_partial_disk_retains_full_fixed_capacity_and_original_files(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            asset = base / str(uuid.uuid4())
            asset.mkdir(mode=0o700)
            manifest = {"schema_version": 1, "asset_id": asset.name, "instance_id": str(uuid.uuid4()), "org_id": str(uuid.uuid4()), "owner_id": str(uuid.uuid4()), "capacity_bytes": 10 * 2**30}
            (asset / "manifest.json").write_text(json.dumps(manifest))
            (asset / "manifest.json").chmod(0o600)
            (asset / "disk.img").write_bytes(b"partial original")
            (asset / "disk.img").chmod(0o600)
            before = {p.name: p.read_bytes() for p in asset.iterdir()}
            rows = inventory.census(base)
            self.assertFalse(rows[0]["identity_valid"])
            self.assertEqual(rows[0]["capacity_bytes"], 10 * 2**30)
            self.assertEqual(before, {p.name: p.read_bytes() for p in asset.iterdir()})

    def test_directory_symlink_and_duplicate_manifest_fail_closed(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            asset = base / str(uuid.uuid4())
            asset.mkdir(mode=0o700)
            (asset / "manifest.json").write_text('{"schema_version":1,"schema_version":1}')
            (asset / "manifest.json").chmod(0o600)
            link = base / str(uuid.uuid4())
            link.symlink_to(asset)
            rows = inventory.census(base)
            self.assertEqual(len(rows), 2)
            self.assertTrue(all(not row["identity_valid"] for row in rows))

if __name__ == "__main__":
    unittest.main()
