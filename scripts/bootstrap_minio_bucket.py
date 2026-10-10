#!/usr/bin/env python3
"""
Idempotently ensure a MinIO bucket exists.

Default endpoint: from compose network -> http://minio:9000
                  from host          -> http://localhost:9100
Default creds:    MINIO_ACCESS_KEY=minioadmin, MINIO_SECRET_KEY=devminio
Default bucket:   MINIO_BUCKET_NAME=luosa-files

Usage:
  scripts/bootstrap_minio_bucket.py             # use defaults (env vars optional)
  MINIO_ENDPOINT=http://minio:9000 MINIO_BUCKET_NAME=foo scripts/bootstrap_minio_bucket.py
"""

import os
import sys

import boto3
from botocore.exceptions import ClientError

ENDPOINT = os.environ.get("MINIO_ENDPOINT", "http://minio:9000")
ACCESS = os.environ.get("MINIO_ACCESS_KEY") or os.environ.get("MINIO_ACCESS_KEY_ID") or "minioadmin"
SECRET = (
    os.environ.get("MINIO_SECRET_KEY")
    or os.environ.get("MINIO_SECRET_ACCESS_KEY")
    or "devminio"
)
BUCKET = os.environ.get("MINIO_BUCKET_NAME", "luosa-files")


def main() -> int:
    s3 = boto3.client(
        "s3",
        endpoint_url=ENDPOINT,
        aws_access_key_id=ACCESS,
        aws_secret_access_key=SECRET,
        region_name="us-east-1",
    )
    try:
        existing = {b["Name"] for b in s3.list_buckets().get("Buckets", [])}
    except ClientError as e:
        print(f"[FAIL] list_buckets: {e}", file=sys.stderr)
        return 1

    if BUCKET in existing:
        print(f"[OK] bucket already exists: {BUCKET} (endpoint={ENDPOINT})")
        return 0

    try:
        s3.create_bucket(Bucket=BUCKET)
        print(f"[OK] created bucket: {BUCKET} (endpoint={ENDPOINT})")
        return 0
    except ClientError as e:
        code = e.response.get("Error", {}).get("Code", "")
        if code in {"BucketAlreadyOwnedByYou", "BucketAlreadyExists"}:
            print(f"[OK] bucket already exists (race): {BUCKET}")
            return 0
        print(f"[FAIL] create_bucket: {e}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
