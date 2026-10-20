"""Read-only inventory of platform asset metadata; never mounts a filesystem."""
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess

UUID = re.compile(r"^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$")
FIELDS = {"schema_version", "asset_id", "instance_id", "org_id", "owner_id", "capacity_bytes"}

def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate metadata")
        result[key] = value
    return result

def census(base=Path("/assets")):
    result = []
    entries = list(base.iterdir())
    if len(entries) > 1000:
        raise ValueError("inventory limit")
    for path in entries:
        resource = {"kind": "ASSET", "external_id": path.name if UUID.fullmatch(path.name) else "unknown-" + hashlib.sha256(path.name.encode()).hexdigest(), "capacity_bytes": 100 * 2**30, "identity_valid": False}
        try:
            info = path.lstat()
            if not UUID.fullmatch(path.name) or not stat.S_ISDIR(info.st_mode) or info.st_mode & 0o077:
                raise ValueError("asset directory")
            # NOFOLLOW plus a strict size bound avoids following an injected
            # manifest symlink into host secrets, even with the read-only bind.
            fd = os.open(path / "manifest.json", os.O_RDONLY | os.O_NOFOLLOW)
            try:
                info = os.fstat(fd)
                if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077 or info.st_size > 4096:
                    raise ValueError("manifest file")
                raw = os.read(fd, 4097)
            finally:
                os.close(fd)
            manifest = json.loads(raw, object_pairs_hook=unique_object)
            if not isinstance(manifest, dict) or set(manifest) != FIELDS or type(manifest["schema_version"]) is not int or manifest["schema_version"] != 1 or type(manifest["capacity_bytes"]) is not int or (not 2**30 <= manifest["capacity_bytes"] <= 100 * 2**30 or manifest["capacity_bytes"] % 2**30) or any(not isinstance(manifest[k], str) or not UUID.fullmatch(manifest[k]) for k in ("asset_id", "instance_id", "org_id", "owner_id")) or manifest["asset_id"] != path.name:
                raise ValueError("manifest identity")
            resource.update({"instance_id": manifest["instance_id"], "asset_id": manifest["asset_id"], "organization_id": manifest["org_id"], "owner_id": manifest["owner_id"]})
            disk = path / "disk.img"
            info = disk.lstat()
            if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077 or info.st_nlink != 1:
                raise ValueError("disk file")
            # st_blocks may include host filesystem allocation overhead. The
            # hard work-disk capacity is its logical size, with a fixed floor.
            resource["capacity_bytes"] = max(manifest["capacity_bytes"], info.st_size)
            if info.st_size != manifest["capacity_bytes"] or info.st_blocks * 512 < manifest["capacity_bytes"]:
                raise ValueError("disk capacity")
            # Read only the ext4 superblock UUID, not user files or sessions.
            fsid = subprocess.run(["blkid", "-p", "-s", "UUID", "-o", "value", str(disk)], check=True, capture_output=True, text=True, timeout=5).stdout.strip()
            resource["identity_valid"] = fsid == path.name
        except (OSError, ValueError, KeyError, subprocess.SubprocessError):
            pass
        result.append(resource)
    return result

if __name__ == "__main__":
    print(json.dumps(census(), separators=(",", ":")))
