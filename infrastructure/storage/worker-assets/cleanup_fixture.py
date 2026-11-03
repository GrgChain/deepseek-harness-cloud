import os
import subprocess
import shutil

p = "/host-dev/hc-cloud/" + os.environ["ASSET"]
if os.path.islink(p):
    d = os.readlink(p)
    subprocess.run(["losetup", "-d", d], check=True)
    os.unlink(p)
a = "/assets/" + os.environ["ASSET"]
if os.path.isdir(a):
    shutil.rmtree(a)
