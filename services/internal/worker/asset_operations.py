"""Trusted offline disk operations, embedded in hcctl; never in member images.

The caller holds the host instance guard; restore and reclaim also require a
persisted maintenance intent. Browsing exposes only two fixed roots read-only.
"""
import fcntl
import gzip
import importlib.util
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
from datetime import datetime, timezone

spec = importlib.util.spec_from_file_location("storage", "/opt/harness-storage/helper.py")
storage = importlib.util.module_from_spec(spec)
spec.loader.exec_module(storage)
patch_storage(storage)
BASE = Path("/assets")
DEVICES = Path("/host-dev/hc-cloud")
PHASE = "CONTRACT"
ROOTS = {"workspace": "workspace", "dsh": "dsh"}
PREVIEW_MAX = 512 * 1024
DOWNLOAD_MAX = 64 * 1024 * 1024
DIRECTORY_MAX = 10000
DIRECTORY_PAGE = 100

class BrowseFailure(Exception):
    def __init__(self, phase): self.phase = phase

def path_parts(value, allow_empty):
    if not isinstance(value, str) or len(value) > 4096 or value.startswith('/') or value.endswith('/') or '\\' in value or '\0' in value:
        raise BrowseFailure('BROWSE_FILE_UNAVAILABLE')
    if value == '':
        if allow_empty: return []
        raise BrowseFailure('BROWSE_FILE_UNAVAILABLE')
    parts = value.split('/')
    if any(not part or part in ('.', '..') or len(os.fsencode(part)) > 255 for part in parts):
        raise BrowseFailure('BROWSE_FILE_UNAVAILABLE')
    return parts

def open_directory(mountpoint, root, parts):
    fd = os.open(mountpoint, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        next_fd = os.open(ROOTS[root], os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
    except Exception:
        os.close(fd)
        raise
    os.close(fd)
    fd = next_fd
    for part in parts:
        try:
            next_fd = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
        except Exception:
            os.close(fd)
            raise BrowseFailure('BROWSE_FILE_UNAVAILABLE')
        os.close(fd)
        fd = next_fd
    return fd

def browse_filesystem(mountpoint, request):
    mode, root, path = request.get('mode'), request.get('root'), request.get('path')
    if mode not in ('LIST', 'PREVIEW', 'DOWNLOAD') or root not in ROOTS:
        raise BrowseFailure('BROWSE_FILE_UNAVAILABLE')
    parts = path_parts(path, mode == 'LIST')
    if mode == 'LIST':
        after = request.get('after', '')
        if not isinstance(after, str) or len(os.fsencode(after)) > 255 or '/' in after or '\\' in after or '\0' in after:
            raise BrowseFailure('BROWSE_FILE_UNAVAILABLE')
        directory = open_directory(mountpoint, root, parts)
        try:
            names = []
            for name in os.listdir(directory):
                try: encoded = name.encode('utf-8', 'strict')
                except UnicodeEncodeError: continue
                if encoded and name > after: names.append(name)
            if len(names) > DIRECTORY_MAX:
                raise BrowseFailure('BROWSE_DIRECTORY_TOO_LARGE')
            names.sort()
            more = len(names) > DIRECTORY_PAGE
            names = names[:DIRECTORY_PAGE]
            entries = []
            for name in names:
                info = os.stat(name, dir_fd=directory, follow_symlinks=False)
                if stat.S_ISDIR(info.st_mode): kind = 'DIRECTORY'
                elif stat.S_ISREG(info.st_mode): kind = 'FILE'
                elif stat.S_ISLNK(info.st_mode): kind = 'LINK'
                else: kind = 'OTHER'
                entries.append({'name': name, 'type': kind, 'size_bytes': info.st_size if kind == 'FILE' else 0,
                                'modified_at': datetime.fromtimestamp(info.st_mtime, timezone.utc).isoformat().replace('+00:00', 'Z')})
            result = {'root': root, 'path': path, 'entries': entries}
            if more and entries: result['next_after'] = entries[-1]['name']
            print(json.dumps(result))
        finally:
            os.close(directory)
        return

    parent = open_directory(mountpoint, root, parts[:-1])
    try:
        fd = os.open(parts[-1], os.O_RDONLY | os.O_NOFOLLOW, dir_fd=parent)
    except Exception:
        os.close(parent)
        raise BrowseFailure('BROWSE_FILE_UNAVAILABLE')
    os.close(parent)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode): raise BrowseFailure('BROWSE_FILE_UNAVAILABLE')
        limit = PREVIEW_MAX if mode == 'PREVIEW' else DOWNLOAD_MAX
        if info.st_size > limit: raise BrowseFailure('BROWSE_TOO_LARGE')
        content = bytearray()
        while len(content) <= limit:
            chunk = os.read(fd, min(65536, limit + 1 - len(content)))
            if not chunk: break
            content.extend(chunk)
        if len(content) > limit: raise BrowseFailure('BROWSE_TOO_LARGE')
        after = os.fstat(fd)
        if (info.st_ino, info.st_size, info.st_mtime_ns) != (after.st_ino, after.st_size, after.st_mtime_ns):
            raise BrowseFailure('BROWSE_FILE_UNAVAILABLE')
        if mode == 'PREVIEW':
            try: content.decode('utf-8')
            except UnicodeDecodeError: raise BrowseFailure('BROWSE_PREVIEW_UNAVAILABLE')
            if b'\0' in content: raise BrowseFailure('BROWSE_PREVIEW_UNAVAILABLE')
        sys.stdout.buffer.write(content)
    finally:
        os.close(fd)

def browse_live(request):
    global PHASE
    if set(request) - {'target', 'mode', 'root', 'path', 'after'} or not {'target', 'mode', 'root', 'path'} <= set(request):
        raise BrowseFailure('BROWSE_FILE_UNAVAILABLE')
    target = storage.parse(json.dumps(request['target']))
    PHASE = 'BROWSE_MARKER'
    marker = Path('/data/.hc-initialized')
    try:
        storage.regular_private(marker)
        if storage.parse(marker.read_bytes()) != target:
            raise BrowseFailure('BROWSE_UNAVAILABLE')
    except BrowseFailure:
        raise
    except Exception:
        raise BrowseFailure('BROWSE_UNAVAILABLE')
    PHASE = 'BROWSE_READ'
    browse_filesystem(Path('/data'), request)

def command(*args):
    return subprocess.run(args, check=True, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, text=True, timeout=90).stdout.strip()

def no_mounts(loop):
    info = os.stat(loop)
    device = str(os.major(info.st_rdev)) + ":" + str(os.minor(info.st_rdev))
    # Check each distinct host mount namespace, including Docker daemon mounts.
    namespaces = set()
    processes = list(Path('/proc').glob('[0-9]*'))
    if len(processes) > 65536: raise ValueError('Unbounded mount namespaces')
    for process in processes:
        try:
            identity = (process / 'ns/mnt').stat().st_ino
            if identity in namespaces: continue
            raw = (process / 'mountinfo').read_text()
            if len(raw) > 8 * 1024 * 1024: raise ValueError('Unbounded mount table')
            namespaces.add(identity)
            if any(line.split()[2] == device for line in raw.splitlines()):
                raise ValueError('Asset remains mounted')
        except (FileNotFoundError, ProcessLookupError):
            continue

def attached(image):
    value = json.loads(command("losetup", "--json", "--list", "--associated", str(image), "--output", "NAME"))
    names = [entry["name"] for entry in value.get("loopdevices", [])]
    if len(names) > 1 or any(not re.fullmatch(r"/dev/loop[0-9]+", name) for name in names):
        raise ValueError("Ambiguous owned device")
    return names

def no_orphan_backing_file(asset_id):
    value=json.loads(command('losetup','--json','--list','--output','NAME,BACK-FILE'))
    entries=value.get('loopdevices',[])
    if len(entries)>65536: raise ValueError('Unbounded loop inventory')
    suffix='/'+asset_id+'/disk.img'
    if any(str(entry.get('back-file','')).removesuffix(' (deleted)').endswith(suffix) for entry in entries):
        raise ValueError('Unreconciled original backing file')

def detach(image, asset_id):
    device = DEVICES / asset_id
    loops = attached(image)
    if device.is_symlink():
        if loops != [os.readlink(device)]:
            raise ValueError("Device identity changed")
    elif device.exists() or loops:
        raise ValueError("Missing owned device reference")
    for loop in loops:
        no_mounts(loop)
        command("losetup", "-d", loop)
    if attached(image):
        raise ValueError("Device remains attached")
    if device.is_symlink():
        device.unlink()

def execute(action, request):
    global PHASE
    if set(request) - {"target", "source", "backup_sha256", "mode", "root", "path", "after"}:
        raise ValueError("Unknown operation field")
    target = storage.parse(json.dumps(request["target"]))
    source = storage.parse(json.dumps(request["source"])) if "source" in request else None
    if action not in ["archive", "restore", "reclaim", "browse"]:
        raise ValueError("Unknown operation")
    if action == "restore":
        if source is None or target["asset_id"] == source["asset_id"] or target["instance_id"] == source["instance_id"] or target["owner_id"] != source["owner_id"] or target["org_id"] != source["org_id"] or target["capacity_bytes"] != source["capacity_bytes"] or not re.fullmatch(r"[a-f0-9]{64}", request.get("backup_sha256", "")):
            raise ValueError("Invalid recovery binding")
    elif action == "browse":
        if source is not None or "backup_sha256" in request or not {'mode', 'root', 'path'} <= set(request):
            raise ValueError("Invalid browse request")
        if set(request) - {'target', 'mode', 'root', 'path', 'after'}:
            raise ValueError("Unexpected browse field")
    elif source is not None or "backup_sha256" in request:
        raise ValueError("Unexpected recovery identity")
    storage.private_directory(BASE)
    asset = BASE / target["asset_id"]
    if action == "reclaim" and not asset.exists():
        if os.path.lexists(DEVICES / target["asset_id"]):
            raise ValueError("Unreconciled original device")
        no_orphan_backing_file(target['asset_id'])
        return
    if action == "reclaim" and not (asset / "disk.img").exists():
        storage.private_directory(asset)
        if os.path.lexists(DEVICES / target["asset_id"]): raise ValueError("Unreconciled device")
        no_orphan_backing_file(target['asset_id'])
        if set(p.name for p in asset.iterdir()) - {"manifest.json", ".lock"}: raise ValueError("Unexpected remainder")
        manifest = asset / "manifest.json"
        if manifest.exists():
            storage.regular_private(manifest)
            if storage.parse(manifest.read_bytes()) != target: raise ValueError("Original manifest changed")
        for name in ["manifest.json", ".lock"]:
            path = asset / name
            if path.exists():
                storage.regular_private(path)
                path.unlink()
        asset.rmdir()
        fd = os.open(BASE, os.O_RDONLY | os.O_DIRECTORY)
        try: os.fsync(fd)
        finally: os.close(fd)
        return
    PHASE = "INSPECT_ORIGINAL"
    storage.execute("inspect", target)
    disk = asset / "disk.img"
    lock = os.open(asset / ".lock", os.O_RDWR | os.O_NOFOLLOW)
    stage = None
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        loops = attached(disk)
        if loops != [os.readlink(DEVICES / target["asset_id"])]:
            raise ValueError("Original device mismatch")
        PHASE = "ORIGINAL_IDLE"
        no_mounts(loops[0])
        if action == "archive":
            with disk.open("rb") as file, gzip.GzipFile(fileobj=sys.stdout.buffer, mode="wb", compresslevel=1, mtime=0) as output:
                while chunk := file.read(1024 * 1024):
                    output.write(chunk)
            return
        if action == "browse":
            mountpoint = Path('/mnt/data')
            mountpoint.mkdir(mode=0o700)
            PHASE = 'BROWSE_MOUNT'
            command('mount', '-t', 'ext4', '-o', 'ro,noload,nosuid,nodev', loops[0], str(mountpoint))
            try:
                PHASE = 'BROWSE_READ'
                browse_filesystem(mountpoint, request)
            finally:
                command('umount', str(mountpoint))
            return
        if action == "reclaim":
            if set(p.name for p in asset.iterdir()) != {"disk.img", "manifest.json", ".lock"}:
                raise ValueError("Unrecognized asset files")
            detach(disk, target["asset_id"])
            # Exact owned files only; never recursive deletion of arbitrary paths.
            disk.unlink()
            (asset / "manifest.json").unlink()
            (asset / ".lock").unlink()
            asset.rmdir()
            fd = os.open(BASE, os.O_RDONLY | os.O_DIRECTORY)
            try: os.fsync(fd)
            finally: os.close(fd)
            return
        PHASE = "STAGE_ALLOCATION"
        stage = asset / ("restore-" + request["backup_sha256"] + ".img")
        if stage.exists():
            storage.regular_private(stage)
            if stage.stat().st_size>target["capacity_bytes"]: raise ValueError('Invalid interrupted recovery size')
            for prior in attached(stage):
                loop_nodes(int(prior.removeprefix('/dev/loop')))
                no_mounts(prior)
                command('losetup','-d',prior)
            if attached(stage): raise ValueError('Interrupted recovery remains attached')
            stage.unlink()
        fd = os.open(stage, os.O_CREAT | os.O_EXCL | os.O_RDWR | os.O_NOFOLLOW, 0o600)
        try:
            os.posix_fallocate(fd, 0, target["capacity_bytes"])
            PHASE = "DECOMPRESS"
            total = 0
            with os.fdopen(fd, "wb", closefd=False) as output, gzip.GzipFile(fileobj=sys.stdin.buffer, mode="rb") as archive:
                while chunk := archive.read(1024 * 1024):
                    total += len(chunk)
                    if total > target["capacity_bytes"]: raise ValueError("Backup exceeds target capacity")
                    output.write(chunk)
                if total != target["capacity_bytes"]: raise ValueError("Incomplete filesystem backup")
                output.flush()
                os.fsync(output.fileno())
        finally:
            os.close(fd)
        PHASE = "SOURCE_FILESYSTEM"
        if command("blkid", "-s", "UUID", "-o", "value", str(stage)) != source["asset_id"]:
            raise ValueError("Backup filesystem identity mismatch")
        PHASE = "STAGE_FSCK"
        checked = subprocess.run(["e2fsck", "-f", "-p", str(stage)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=90)
        if checked.returncode not in [0, 1]: raise ValueError("Recovery filesystem check failed")
        PHASE = "REWRITE_FILESYSTEM_ID"
        command("tune2fs", "-U", target["asset_id"], str(stage))
        PHASE = "STAGE_ATTACH"
        loop = attach_owned_loop(str(stage))
        if not re.fullmatch(r"/dev/loop[0-9]+", loop): raise ValueError("Invalid staging device")
        mounted = False
        mountpoint = Path("/mnt/recovery")
        mountpoint.mkdir(mode=0o700)
        try:
            discard = Path("/sys/block") / Path(loop).name / "queue/discard_max_bytes"
            discard.write_text("0")
            if discard.read_text().strip() != "0": raise ValueError("Discard remains enabled")
            PHASE = "STAGE_MOUNT"
            command("mount", "-t", "ext4", "-o", "nosuid,nodev", loop, str(mountpoint))
            mounted = True
            PHASE = "SOURCE_MARKER"
            marker = mountpoint / ".hc-initialized"
            storage.regular_private(marker)
            if storage.parse(marker.read_bytes()) != source: raise ValueError("Backup owner marker mismatch")
            PHASE = "TARGET_MARKER"
            storage.atomic(marker, target)
            os.chown(marker, 1000, 1000)
            command("sync", "-f", str(mountpoint))
        finally:
            if mounted: command("umount", str(mountpoint))
            command("losetup", "-d", loop)
        PHASE = "STAGE_COMMITMENT"
        storage.regular_private(stage)
        if stage.stat().st_blocks * 512 < target["capacity_bytes"]: raise ValueError("Recovery allocation lost")
        PHASE = "DETACH_TARGET"
        detach(disk, target["asset_id"])
        PHASE = "ATOMIC_REPLACE"
        os.replace(stage, disk)
        stage = None
        fd = os.open(asset, os.O_RDONLY | os.O_DIRECTORY)
        try: os.fsync(fd)
        finally: os.close(fd)
    finally:
        if stage is not None and stage.exists(): stage.unlink()
        os.close(lock)
    # Rebind the existing target's stable device reference and verify its marker.
    PHASE = "TARGET_REBIND"
    if action == "restore": storage.execute("inspect", target)

if __name__ == "__main__":
    try:
        if len(sys.argv) != 3: raise ValueError("Bound operation required")
        request = json.loads(sys.argv[2], object_pairs_hook=storage.unique_object)
        if sys.argv[1] == 'browse-live': browse_live(request)
        else: execute(sys.argv[1], request)
    except Exception as error:
        if isinstance(error, BrowseFailure): PHASE = error.phase
        elif len(sys.argv) > 1 and sys.argv[1] in ('browse', 'browse-live') and PHASE.startswith('BROWSE_'):
            PHASE = 'BROWSE_UNAVAILABLE'
        if isinstance(error, subprocess.CalledProcessError) and PHASE == "REWRITE_FILESYSTEM_ID":
            message = (error.stdout or "") + (error.stderr or "")
            if "e2fsck" in message: PHASE = "REWRITE_REQUIRES_FSCK"
            elif "mounted" in message.lower(): PHASE = "REWRITE_MOUNTED"
            elif "unsupported" in message.lower(): PHASE = "REWRITE_UNSUPPORTED"
            elif "uuid" in message.lower(): PHASE = "REWRITE_UUID"
        action = sys.argv[1] if len(sys.argv) > 1 else ""
        preservation = "read-only data retained" if action in ("browse", "browse-live") else "maintenance intent retained"
        message = "storage browse failed" if action == "browse-live" else "offline asset operation failed"
        print("harness-cloud: " + message + " [" + PHASE + "]; " + preservation, file=sys.stderr)
        sys.exit(1)
