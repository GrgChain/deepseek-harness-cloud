import os

fd = os.open(
    "/assets/" + os.environ["ASSET"] + "/restore-" + os.environ["DIGEST"] + ".img",
    os.O_CREAT | os.O_EXCL | os.O_RDWR,
    0o600,
)
os.ftruncate(fd, 1048576)
os.close(fd)
