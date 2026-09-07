#!/usr/bin/env python3
"""Every NEXSPENCE_* the Helm chart sets must name a real config key.

Viper derives an environment variable from the config key path
(auth.jwt_secret -> NEXSPENCE_AUTH_JWT_SECRET), and an env name that matches
no key is simply ignored: the value in values.yaml never reaches the server
and nothing says so. That has bitten the chart three times — S3 credentials
under access_key instead of access_key_id, force_path_style sent as
use_ssl, and http.addr sent as http_listen — each one leaving an install
running on defaults while values.yaml plainly said otherwise.

Renders the chart across its configurations and fails on any env name that
does not correspond to a v.SetDefault key in internal/config/config.go.
"""

import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CHART = ROOT / "deploy" / "helm" / "nexspence"

# The chart renders under several toggles; an env only set on one path
# (S3, scanning, the subdomain connector) is checked the same as the rest.
RENDERS = [
    [],
    ["--set", "scanning.enabled=true"],
    ["--set", "storage.type=s3", "--set", "storage.s3.bucket=b"],
    ["--set", "config.docker.subdomainConnector.enabled=true"],
]


def config_keys() -> set[str]:
    src = (ROOT / "internal" / "config" / "config.go").read_text()
    keys = set(re.findall(r'v\.SetDefault\("([a-z0-9_.]+)"', src))
    if not keys:
        sys.exit("no SetDefault keys found in internal/config/config.go")
    return {"NEXSPENCE_" + k.upper().replace(".", "_") for k in keys}


def rendered_envs() -> set[str]:
    found: set[str] = set()
    for extra in RENDERS:
        out = subprocess.run(
            ["helm", "template", "check", str(CHART), *extra],
            capture_output=True, text=True, check=True,
        ).stdout
        for line in out.splitlines():
            # Comments in the templates mention the wrong names on purpose,
            # to explain what they were; only real keys count.
            stripped = line.strip()
            if stripped.startswith("#"):
                continue
            found.update(re.findall(r"\bNEXSPENCE_[A-Z0-9_]+\b", stripped))
    return found


def main() -> int:
    known = config_keys()
    # The image's entrypoint reads this one; it is not a viper key.
    exempt = {"NEXSPENCE_JWT_SECRET_FILE"}
    unknown = sorted(e for e in rendered_envs() - exempt if e not in known)
    if unknown:
        print("Helm sets environment variables that match no config key:")
        for e in unknown:
            print(f"  {e}")
        print("\nViper derives the name from the key path in "
              "internal/config/config.go, so these are silently ignored.")
        return 1
    print("all chart environment variables map to a config key")
    return 0


if __name__ == "__main__":
    sys.exit(main())
