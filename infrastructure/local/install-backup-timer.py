"""Install only this workspace's generated user timer; no secrets in unit files."""
import os
import pathlib
import re
import stat
import subprocess

root = pathlib.Path(__file__).resolve().parents[2]
if not re.fullmatch(r"/[A-Za-z0-9_.:/-]+", str(root)):
    raise SystemExit("Controlled workspace path required")
private = root / ".local"
if stat.S_IMODE(private.stat().st_mode) != 0o700 or private.is_symlink():
    raise SystemExit("Private workspace configuration required")
required = ("migrator-dsn", "worker-config.json", "auth/data-key", "auth/backup-key", "auth/organization-id")
for name in required:
    path = private / name
    mode = path.lstat().st_mode
    if not stat.S_ISREG(mode) or stat.S_IMODE(mode) & 0o077 or path.stat().st_uid != os.geteuid():
        raise SystemExit("Private owned backup configuration required")
org = (private / "auth/organization-id").read_text().strip()
if not re.fullmatch(r"[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}", org):
    raise SystemExit("Controlled organization required")
env = {
    "HC_DB_DSN_FILE": private / "migrator-dsn",
    "HC_WORKER_CONFIG_FILE": private / "worker-config.json",
    "HC_DATA_KEY_FILE": private / "auth/data-key",
    "HC_BACKUP_KEY_FILE": private / "auth/backup-key",
    "HC_ORGANIZATION_ID": org,
    "HC_OPERATOR_REF": "local-daily-timer",
    "HC_MAINTENANCE": "true",
}
repository = private / "daily-backups"
repository.mkdir(mode=0o700, exist_ok=True)
if repository.is_symlink() or stat.S_IMODE(repository.stat().st_mode) != 0o700:
    raise SystemExit("Private dedicated backup repository required")
config = private / "daily-backup.env"
contents = "# Local stopped-environment rehearsal; values are paths and safe metadata.\n"
contents += "".join(f"{name}={value}\n" for name, value in env.items())
if config.exists() and (config.is_symlink() or not stat.S_ISREG(config.lstat().st_mode) or stat.S_IMODE(config.stat().st_mode) & 0o077 or config.read_text() != contents):
    raise SystemExit("Existing backup configuration differs; review before replacing")
if not config.exists():
    fd = os.open(config, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w") as output:
        output.write(contents)
        output.flush()
        os.fsync(output.fileno())
units = pathlib.Path.home() / ".config/systemd/user"
units.mkdir(parents=True, exist_ok=True)
service = (root / "deploy/local/hc-local-backup.service.in").read_text().replace("@WORKSPACE@", str(root))
timer = (root / "deploy/local/hc-local-backup.timer").read_text()
for name, body in (("hc-local-backup.service", service), ("hc-local-backup.timer", timer)):
    path = units / name
    if path.exists():
        if path.is_symlink() or path.read_text() != body:
            raise SystemExit("Existing user backup unit differs; no unit overwritten")
    else:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as output:
            output.write(body)
subprocess.run(["systemd-analyze", "--user", "verify", str(units / "hc-local-backup.service"), str(units / "hc-local-backup.timer")], check=True, stdout=subprocess.DEVNULL)
subprocess.run(["systemctl", "--user", "daemon-reload"], check=True)
subprocess.run(["systemctl", "--user", "enable", "--now", "hc-local-backup.timer"], check=True, stdout=subprocess.DEVNULL)
print("Local daily backup timer installed; running instances are never stopped automatically")
