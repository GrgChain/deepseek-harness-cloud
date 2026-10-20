"""Bounded loop attachment for privileged trusted helpers only.

Docker snapshots available device nodes when a privileged container starts.
LOOP_CTL_GET_FREE can subsequently allocate a higher loop minor whose /dev node
is absent in that container. Create only that kernel-selected block node, and
reconcile the exact backing file after every attempted attachment.
"""
import fcntl
import json
import os
from pathlib import Path
import re
import stat
import subprocess

def loop_names(path):
    result = subprocess.run(['losetup', '--json', '--list', '--associated', str(path), '--output', 'NAME'], check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=10)
    names = [entry['name'] for entry in json.loads(result.stdout).get('loopdevices', [])]
    if len(names)>1 or any(not re.fullmatch(r'/dev/loop[0-9]+',name) for name in names):
        raise ValueError('Ambiguous original loop attachment')
    return names

def loop_nodes(minor):
    for root in ['/dev', '/host-dev']:
        node = Path(root) / ('loop' + str(minor))
        try: os.mknod(node, stat.S_IFBLK | 0o600, os.makedev(7, minor))
        except FileExistsError: pass
        info = node.stat()
        if not stat.S_ISBLK(info.st_mode) or info.st_rdev != os.makedev(7, minor):
            raise ValueError('Loop node identity mismatch')

def attach_owned_loop(image):
    path = Path(image)
    if not path.is_absolute() or path.parent.parent != Path('/assets'):
        raise ValueError('Unbound loop image')
    existing=loop_names(path)
    if existing:
        minor=int(existing[0].removeprefix('/dev/loop'))
        if not 0 <= minor < 1048576: raise ValueError('Invalid original loop minor')
        loop_nodes(minor)
        return existing[0]
    control = os.open('/host-dev/loop-control', os.O_RDWR)
    try:
        for _ in range(8):
            minor = fcntl.ioctl(control, 0x4C82)  # Linux LOOP_CTL_GET_FREE
            if not 0 <= minor < 1048576: raise ValueError('Invalid kernel loop minor')
            loop_nodes(minor)
            loop = '/dev/loop' + str(minor)
            subprocess.run(['losetup', '--nooverlap', loop, str(path)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10)
            names = loop_names(path)
            if names == [loop]: return loop
            if names: raise ValueError('Ambiguous original loop attachment')
        raise ValueError('Loop allocation remained busy')
    finally:
        os.close(control)

def patch_storage(module):
    original = module.command
    def command(*args):
        if args[:4] == ('losetup', '--find', '--show', '--nooverlap'):
            if len(args) != 5: raise ValueError('Unbound loop attachment')
            return attach_owned_loop(args[4])
        return original(*args)
    module.command = command
