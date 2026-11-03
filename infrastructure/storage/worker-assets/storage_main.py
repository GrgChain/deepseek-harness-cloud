"""Storage helper entry point, appended to storage_loop.py by the host."""
import json
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from storage_loop import patch_storage

import importlib.util
import sys

spec = importlib.util.spec_from_file_location("storage", "/opt/harness-storage/helper.py")
storage = importlib.util.module_from_spec(spec)
spec.loader.exec_module(storage)
patch_storage(storage)
try:
    if len(sys.argv) != 2:
        raise ValueError("One action required")
    data = sys.stdin.buffer.read(65537)
    if len(data) > 65536:
        raise ValueError("Request too large")
    print(json.dumps(storage.execute(sys.argv[1], storage.parse(data))))
except Exception:
    print(
        "harness-cloud: storage operation failed [" + storage.PHASE + "]; original data retained",
        file=sys.stderr,
    )
    sys.exit(1)
