"""Trusted host storage executor. Never mounted or callable by member containers.

The caller must hold the PostgreSQL asset lease before executing this helper.
Only preparation and inspection are implemented; reclamation is deliberately
separate from workspace destruction.
"""
import fcntl
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile

GiB = 2**30
MAX_CAPACITY = 100 * GiB
UUID = re.compile(r"^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$")
FIELDS = {"schema_version", "asset_id", "instance_id", "org_id", "owner_id", "capacity_bytes"}
PHASE = "CONTRACT"

def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("Duplicate field")
        result[key] = value
    return result

def parse(data):
    value = json.loads(data, object_pairs_hook=unique_object)
    if not isinstance(value, dict) or set(value) != FIELDS or type(value["schema_version"]) is not int or value["schema_version"] != 1 or type(value["capacity_bytes"]) is not int or not GiB <= value["capacity_bytes"] <= MAX_CAPACITY or value["capacity_bytes"] % GiB:
        raise ValueError("Invalid storage contract")
    if any(not isinstance(value[key], str) or not UUID.fullmatch(value[key]) for key in ["asset_id", "instance_id", "org_id", "owner_id"]):
        raise ValueError("Invalid storage identity")
    return value

def regular_private(path):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077:
        raise ValueError("Private regular file required")
    return info

def private_directory(path):
    info = path.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_mode & 0o077:
        raise ValueError("Private directory required")

def command(*args):
    return subprocess.run(args, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=90).stdout.strip()

def atomic(path, value):
    fd, name = tempfile.mkstemp(prefix=".hc-", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as file:
            json.dump(value, file)
            file.flush()
            os.fsync(file.fileno())
        os.replace(name, path)
        fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
    finally:
        if os.path.exists(name):
            os.unlink(name)

def execute(action, config, base=Path("/assets"), devices=Path("/host-dev/hc-cloud"), mountpoint=Path("/mnt/asset")):
    global PHASE
    PHASE = "ASSET_DIRECTORY"
    if action not in ["prepare", "inspect"]:
        raise ValueError("Unsupported storage action")
    private_directory(base)
    asset = base / config["asset_id"]
    if action == "prepare":
        # O_EXCL directory creation fences first initialization. A partial
        # preparation fails closed and requires an explicit operator repair.
        asset.mkdir(mode=0o700)
    private_directory(asset)
    lock = os.open(asset / ".lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    loop = None
    mounted = False
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        image = asset / "disk.img"
        if action == "prepare":
            fd = os.open(image, os.O_CREAT | os.O_EXCL | os.O_RDWR | os.O_NOFOLLOW, 0o600)
            try:
                os.posix_fallocate(fd, 0, config["capacity_bytes"])
                os.fsync(fd)
            finally:
                os.close(fd)
            # mkfs normally discards unused blocks and makes a preallocated
            # image sparse again. Preserve the physical capacity commitment.
            # Lazy inode initialization uses block zeroout after mounting. Loop
            # devices can implement that by punching holes in the backing file.
            # Finish initialization now, then recommit any zeroed extents before
            # publishing the independently owned filesystem.
            command("mkfs.ext4", "-q", "-F", "-E", "nodiscard,lazy_itable_init=0,lazy_journal_init=0", "-m", "0", "-U", config["asset_id"], str(image))
            fd = os.open(image, os.O_RDWR | os.O_NOFOLLOW)
            try:
                os.posix_fallocate(fd, 0, config["capacity_bytes"])
                os.fsync(fd)
            finally:
                os.close(fd)
        else:
            PHASE = "ORIGINAL_MANIFEST"
            regular_private(asset / "manifest.json")
            if parse((asset / "manifest.json").read_bytes()) != config:
                raise ValueError("Original asset identity mismatched")
        PHASE = "ORIGINAL_CAPACITY"
        info = regular_private(image)
        if info.st_size != config["capacity_bytes"]:
            PHASE = "ORIGINAL_SIZE"
            raise ValueError("Original asset capacity is missing")
        if info.st_blocks * 512 < config["capacity_bytes"]:
            PHASE = "ORIGINAL_ALLOCATION_" + str(info.st_blocks * 512)
            raise ValueError("Original asset capacity is missing")
        PHASE = "FILESYSTEM_IDENTITY"
        if command("blkid", "-s", "UUID", "-o", "value", str(image)) != config["asset_id"]:
            raise ValueError("Original filesystem identity mismatched")
        PHASE = "DEVICE_ATTACHMENT"
        loop = command("losetup", "--find", "--show", "--nooverlap", str(image))
        if not re.fullmatch(r"/dev/loop[0-9]+", loop):
            raise ValueError("Invalid storage device")
        PHASE = "DEVICE_COMMITMENT"
        # Discard on this owned loop device must never turn the hard reservation
        # into a sparse file, including during a host-wide filesystem trim.
        discard = Path("/sys/block") / Path(loop).name / "queue/discard_max_bytes"
        discard.write_text("0")
        if discard.read_text().strip() != "0":
            raise ValueError("Storage discard remains enabled")
        PHASE = "DEVICE_REFERENCE"
        devices.mkdir(mode=0o700, exist_ok=True)
        device = devices / config["asset_id"]
        if device.is_symlink():
            if os.readlink(device) != loop:
                raise ValueError("Original storage device changed; reconciliation required")
        elif device.exists():
            raise ValueError("Storage device conflicts")
        else:
            os.symlink(loop, device)
        PHASE = "FILESYSTEM_MOUNT"
        mountpoint.mkdir(parents=True, mode=0o700, exist_ok=True)
        # Linux requires the same superblock flags when Docker already holds a
        # live rw mount. Inspection performs no file writes; a second ro block
        # mount would fail with EBUSY against that live original filesystem.
        command("mount", "-t", "ext4", "-o", "nosuid,nodev", loop, str(mountpoint))
        mounted = True
        PHASE = "ORIGINAL_MARKER"
        marker = mountpoint / ".hc-initialized"
        if action == "prepare":
            atomic(marker, config)
            os.chown(marker, 1000, 1000)
            os.chown(mountpoint, 1000, 1000)
            os.chmod(mountpoint, 0o700)
            atomic(asset / "manifest.json", config)
        else:
            regular_private(marker)
            if parse(marker.read_bytes()) != config:
                raise ValueError("Original storage marker mismatched")
        PHASE = "FILESYSTEM_CAPACITY"
        fs = os.statvfs(mountpoint)
        capacity = config["capacity_bytes"]
        if fs.f_blocks * fs.f_frsize > capacity or fs.f_blocks * fs.f_frsize < capacity * 0.9:
            raise ValueError("Filesystem capacity does not match the hard limit")
        return {"filesystem_id": config["asset_id"], "capacity_bytes": capacity, "resource_name": "hc-" + config["asset_id"], "device": "/dev/hc-cloud/" + config["asset_id"]}
    finally:
        if mounted:
            command("umount", str(mountpoint))
        os.close(lock)
        # The loop device remains owned by this independent asset. Compute
        # destruction never detaches or deletes it.

if __name__ == "__main__":
    try:
        if len(sys.argv) != 2:
            raise ValueError("One action required")
        data = sys.stdin.buffer.read(65537)
        if len(data) > 65536:
            raise ValueError("Request too large")
        print(json.dumps(execute(sys.argv[1], parse(data))))
    except Exception:
        print("harness-cloud: storage operation failed [" + PHASE + "]; original data retained", file=sys.stderr)
        sys.exit(1)
