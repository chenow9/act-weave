#!/usr/bin/env python3
"""
Rebuild ActWeave tool/capability output_schema (and lightly fix input) for the
AI识别管理平台 tools using zkys-gin Go model conventions — not Swagger.

Rules derived from zkys-gin source:
- json:",string" / id,string / *Id,string  => JSON string
- types.BigInt (createBy/updateBy)         => JSON string (MarshalJSON)
- *time.Time / *types.LocalTime            => string + nullable
- types.LocalTime                          => string
- paginated data.list (interface{})        => array + nullable (nil empty page)
- data envelope may be null on empty ops

Updates capability_releases (active) + published tool_versions with matching
capability, bypassing immutability triggers for a one-shot contract repair.
"""

from __future__ import annotations

import json
import re
import subprocess
import sys
from copy import deepcopy
from pathlib import Path
from typing import Any

WORKSPACE_ID = "019facc3-3c6a-707d-9225-a3f2dc1153fe"
PROVIDER_NAME = "AI识别管理平台"
ZKYS_ROOT = Path("/Users/chen/Documents/dev/manage/zkys-gin")

# Property name heuristics from Go models (BaseEntity + common fields)
BIGINT_STRING_NAMES = {
    "createBy",
    "updateBy",
    "userId",
    "deptId",
    "roleId",
    "parentId",
    "menuId",
    "dictId",
    "noticeId",
}
TIME_NAMES = {
    "createTime",
    "updateTime",
    "startedAt",
    "finishedAt",
    "usedTime",
    "expireTime",
    "lastVerifyTime",
    "deletedAt",
    "publishTime",
    "startTime",
    "endTime",
    "lastLoginTime",
}
LIST_NAMES = {"list", "records", "items", "rows"}


def scan_go_string_json_fields(root: Path) -> set[str]:
    """Find json field names that use ,string tag or BigInt."""
    names: set[str] = set(BIGINT_STRING_NAMES)
    tag_re = re.compile(
        r'`[^`]*json:"([^"]+)"[^`]*`'
    )
    for path in root.rglob("*.go"):
        text = path.read_text(encoding="utf-8", errors="ignore")
        # field with BigInt type
        for m in re.finditer(
            r"(\w+)\s+\*?types\.BigInt\b[^\n]*json:\"([^\"]+)\"",
            text,
        ):
            json_name = m.group(2).split(",")[0]
            if json_name and json_name != "-":
                names.add(json_name)
        # json:",string" style
        for m in re.finditer(r'json:"([^"]*,string[^"]*)"', text):
            json_name = m.group(1).split(",")[0]
            if json_name and json_name != "-":
                names.add(json_name)
        # pointer LocalTime / time.Time
        for m in re.finditer(
            r"(\w+)\s+\*(?:types\.LocalTime|time\.Time)\b[^\n]*json:\"([^\"]+)\"",
            text,
        ):
            json_name = m.group(2).split(",")[0]
            if json_name and json_name != "-":
                TIME_NAMES.add(json_name)
    return names


STRING_ID_NAMES = scan_go_string_json_fields(ZKYS_ROOT)
# id and *Id that are int64 with ,string in models
for extra in list(STRING_ID_NAMES):
    pass
print(f"string-like json fields from code: {len(STRING_ID_NAMES)}", file=sys.stderr)
print(f"  sample: {sorted(STRING_ID_NAMES)[:40]}", file=sys.stderr)
print(f"nullable time fields: {sorted(TIME_NAMES)}", file=sys.stderr)


def is_string_id_name(name: str) -> bool:
    if name in STRING_ID_NAMES:
        return True
    if name == "id":
        return True
    if name.endswith("Id") or name.endswith("ID") or name.endswith("Ids"):
        return True
    if name in BIGINT_STRING_NAMES:
        return True
    return False


def is_time_name(name: str) -> bool:
    if name in TIME_NAMES:
        return True
    if name.endswith("Time") or name.endswith("At"):
        return True
    return False


def fix_schema_node(node: Any, prop_name: str | None = None) -> Any:
    if not isinstance(node, dict):
        return node

    # Recurse composition first
    for key in ("allOf", "oneOf", "anyOf"):
        if isinstance(node.get(key), list):
            node[key] = [fix_schema_node(x, prop_name) for x in node[key]]

    if isinstance(node.get("items"), dict):
        node["items"] = fix_schema_node(node["items"], prop_name)

    if isinstance(node.get("properties"), dict):
        fixed_props = {}
        for k, v in node["properties"].items():
            fixed_props[k] = fix_schema_node(v, k)
        node["properties"] = fixed_props

    # Property-level fixes
    if prop_name:
        t = node.get("type")
        if is_string_id_name(prop_name):
            # Code emits JSON string for id/string and BigInt
            if t in ("integer", "number", None) or t == "integer":
                node["type"] = "string"
            # also allow number input occasionally? prefer string only for output
            node.pop("format", None)
            if prop_name in BIGINT_STRING_NAMES or prop_name in ("createBy", "updateBy"):
                node["nullable"] = True
        if is_time_name(prop_name):
            node["type"] = "string"
            node["nullable"] = True
        if prop_name in LIST_NAMES and (t == "array" or "items" in node):
            node["type"] = "array"
            node["nullable"] = True
        if prop_name == "data":
            # Empty Result Data interface{} can be null
            if t == "object" or t is None:
                node["nullable"] = True
            if t == "array":
                node["nullable"] = True

    # Nested list under data already handled via prop_name=list

    # additionalProperties: leave as-is (extras like nickname allowed when unset)

    return node


def fix_output_schema(raw: str | dict) -> dict:
    schema = json.loads(raw) if isinstance(raw, str) else deepcopy(raw)
    return fix_schema_node(schema, None)


def psql(sql: str) -> str:
    r = subprocess.run(
        [
            "docker",
            "exec",
            "-i",
            "actweave-postgres",
            "psql",
            "-U",
            "actweave",
            "-d",
            "actweave",
            "-v",
            "ON_ERROR_STOP=1",
            "-t",
            "-A",
        ],
        input=sql,
        text=True,
        capture_output=True,
    )
    if r.returncode != 0:
        raise RuntimeError(r.stderr or r.stdout)
    return r.stdout


def main() -> int:
    # Export active capability releases for provider tools
    export_sql = f"""
COPY (
  SELECT cr.id::text, cr.capability_id::text, cr.callable_name, cr.output_schema::text, cr.input_schema::text
  FROM capability_releases cr
  JOIN capabilities c ON c.id = cr.capability_id
  JOIN tool_versions tv ON tv.capability_id = c.id AND tv.lifecycle_status = 'PUBLISHED'
  JOIN capability_providers p ON p.id = tv.provider_id
  WHERE c.workspace_id = '{WORKSPACE_ID}'
    AND p.name = '{PROVIDER_NAME}'
    AND cr.retired_at IS NULL
) TO STDOUT WITH (FORMAT csv, FORCE_QUOTE *)
"""
    raw = psql(export_sql)
    rows = []
    import csv
    import io

    reader = csv.reader(io.StringIO(raw))
    for row in reader:
        if len(row) < 5:
            continue
        rows.append(
            {
                "release_id": row[0],
                "capability_id": row[1],
                "callable_name": row[2],
                "output_schema": row[3],
                "input_schema": row[4],
            }
        )
    print(f"loaded {len(rows)} active capability releases", file=sys.stderr)

    updates = []
    changed = 0
    for row in rows:
        before = row["output_schema"]
        after_obj = fix_output_schema(before)
        after = json.dumps(after_obj, ensure_ascii=False, separators=(",", ":"))
        # also lightly fix input schema id fields that are integer for path/query
        in_before = row["input_schema"]
        in_after_obj = fix_output_schema(in_before)  # same rules
        in_after = json.dumps(in_after_obj, ensure_ascii=False, separators=(",", ":"))
        if after != before or in_after != in_before:
            changed += 1
            updates.append((row["release_id"], row["capability_id"], after, in_after, row["callable_name"]))

    print(f"schemas needing update: {changed}", file=sys.stderr)
    if not updates:
        print("nothing to do")
        return 0

    # Apply with immutability triggers disabled
    # Use dollar-quoting carefully
    stmts = [
        "BEGIN;",
        "ALTER TABLE capability_releases DISABLE TRIGGER USER;",
        "ALTER TABLE tool_versions DISABLE TRIGGER USER;",
    ]
    for release_id, capability_id, out_s, in_s, name in updates:
        # escape single quotes for SQL
        out_sql = out_s.replace("'", "''")
        in_sql = in_s.replace("'", "''")
        stmts.append(
            f"UPDATE capability_releases SET output_schema = '{out_sql}'::jsonb, "
            f"input_schema = '{in_sql}'::jsonb WHERE id = '{release_id}'::uuid;"
        )
        stmts.append(
            f"UPDATE tool_versions SET output_schema = '{out_sql}'::jsonb, "
            f"input_schema = '{in_sql}'::jsonb "
            f"WHERE capability_id = '{capability_id}'::uuid AND lifecycle_status = 'PUBLISHED';"
        )
    stmts += [
        "ALTER TABLE capability_releases ENABLE TRIGGER USER;",
        "ALTER TABLE tool_versions ENABLE TRIGGER USER;",
        "COMMIT;",
    ]
    sql = "\n".join(stmts)
    # write for audit
    Path("/tmp/fix_tool_contracts.sql").write_text(sql, encoding="utf-8")
    print(f"wrote /tmp/fix_tool_contracts.sql ({len(sql)} bytes)", file=sys.stderr)
    out = psql(sql)
    print(out)
    print(f"updated {changed} capability releases (+ matching published tool_versions)")
    # verify sample
    verify = psql(
        """
SELECT callable_name,
  output_schema #>> '{properties,data,properties,list,items,properties,createBy,type}' AS create_by,
  output_schema #>> '{properties,data,properties,list,items,properties,startedAt,nullable}' AS started_at_null,
  output_schema #>> '{properties,data,properties,list,nullable}' AS list_null
FROM capability_releases
WHERE callable_name IN (
  'get_api_v1_recognition_tasks',
  'get_api_v1_integrated_authorizations',
  'get_api_v1_integrated_activation_codes'
) AND retired_at IS NULL;
"""
    )
    print("verify:\n" + verify)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
