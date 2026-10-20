"""Install the pinned Terraform host executable; no Coder image is involved."""
import hashlib
import io
import json
import os
from pathlib import Path
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[2]
ARCHIVE_SHA = "702b2136af6728c8ff037f843dd2dbce2b7ad88786b7381d1d72aefa250f601c"
BINARY_SHA = "d42b544fbd42bfab4f0ddfe1bd8ec985fbd59fb4fb7ff339d5f134c73e177279"
PROVIDERS = [
    ("coder", "coder", "2.19.0", "a9a1106d578f2fe42336ec49dcd0cccd9d23845dbbddfaa00607839564328e12"),
    ("kreuzwerker", "docker", "4.6.0", "866046fa4938ac1ed5cc58b3c4d36e27433dac1aae50256911e6f28184dcf82e"),
]

def main():
    root = ROOT / ".local/workspace-engine"
    root.mkdir(mode=0o700, exist_ok=True)
    info = root.lstat()
    if not root.is_dir() or root.is_symlink() or info.st_mode & 0o077:
        raise RuntimeError("Private engine directory required")
    binary = root / "terraform"
    if not binary.exists():
        with urllib.request.urlopen("https://releases.hashicorp.com/terraform/1.15.5/terraform_1.15.5_linux_amd64.zip", timeout=45) as response:
            archive = response.read(64 * 1024 * 1024 + 1)
        if hashlib.sha256(archive).hexdigest() != ARCHIVE_SHA:
            raise RuntimeError("Terraform archive checksum mismatch")
        executable = zipfile.ZipFile(io.BytesIO(archive)).read("terraform")
        if hashlib.sha256(executable).hexdigest() != BINARY_SHA:
            raise RuntimeError("Terraform executable checksum mismatch")
        fd = os.open(binary, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o700)
        with os.fdopen(fd, "wb") as output:
            output.write(executable)
            output.flush()
            os.fsync(output.fileno())
    if binary.is_symlink() or hashlib.sha256(binary.read_bytes()).hexdigest() != BINARY_SHA:
        raise RuntimeError("Existing Terraform executable differs from approved version")
    for namespace, name, version, sha in PROVIDERS:
        directory = root / "providers/registry.terraform.io" / namespace / name
        directory.mkdir(parents=True, mode=0o700, exist_ok=True)
        filename = f"terraform-provider-{name}_{version}_linux_amd64.zip"
        target = directory / filename
        if not target.exists():
            url = f"https://github.com/{namespace}/terraform-provider-{name}/releases/download/v{version}/{filename}"
            with urllib.request.urlopen(url, timeout=45) as response:
                archive = response.read(128 * 1024 * 1024 + 1)
            if hashlib.sha256(archive).hexdigest() != sha:
                raise RuntimeError("Provider archive checksum mismatch")
            fd = os.open(target, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
            with os.fdopen(fd, "wb") as output:
                output.write(archive)
                output.flush()
                os.fsync(output.fileno())
        if target.is_symlink() or hashlib.sha256(target.read_bytes()).hexdigest() != sha:
            raise RuntimeError("Existing Provider archive differs from approved lock")
    config = root / "terraform.rc"
    if not config.exists():
        fd = os.open(config, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        with os.fdopen(fd, "w") as output:
            output.write('disable_checkpoint = true\nprovider_installation {\n filesystem_mirror { path = ' + json.dumps(str(root / "providers")) + ' }\n}\n')
    options = root / "options.json"
    if not options.exists():
        fd = os.open(options, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        with os.fdopen(fd, "w") as output:
            json.dump({"binary": str(binary), "binary_sha256": BINARY_SHA, "root": str(root), "cli_config": str(config)}, output, indent=2)
    print("Pinned Terraform 1.15.5 prepared for the embedded Go Worker.")

if __name__ == "__main__":
    main()
