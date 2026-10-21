#!/usr/bin/env python3
"""Legacy migration validator. Ownership writes are retired in favor of the Go ORCHESTRATOR."""
import argparse
import contextlib
import datetime as dt
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import uuid

HEADER = "## Atomic Claims\n| Feature | Agent | Token | Worktree | Scopes | Migration | Status | Heartbeat |\n|---|---|---|---|---|---|---|---|\n"


def git(path, *args):
    return subprocess.check_output(["git", "-C", str(path), *args], text=True).strip()


def shared_root(path):
    common = Path(git(path, "rev-parse", "--git-common-dir"))
    if not common.is_absolute():
        common = Path(path) / common
    return common.resolve().parent


@contextlib.contextmanager
def locked(root):
    key = hashlib.sha256(str(root).encode()).hexdigest()[:24]
    with open(Path(tempfile.gettempdir()) / f"listenledger-coordination-{key}.lock", "a") as stream:
        fcntl.flock(stream, fcntl.LOCK_EX)
        yield


def atomic_write(path, text):
    fd, name = tempfile.mkstemp(dir=path.parent, prefix=".coordination-")
    try:
        with os.fdopen(fd, "w") as stream:
            stream.write(text)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def rows(text):
    if HEADER not in text:
        return []
    section = text.split(HEADER, 1)[1].split("\n## ", 1)[0]
    return [list(map(str.strip, line.strip().strip("|").split("|")))
            for line in section.splitlines() if line.startswith("| ")]


def write_rows(text, claims):
    body = "".join("| " + " | ".join(row) + " |\n" for row in claims)
    if HEADER not in text:
        return text + "\n" + HEADER + body
    before, after = text.split(HEADER, 1)
    next_section = after.find("\n## ")
    tail = after[next_section:] if next_section >= 0 else ""
    return before + HEADER + body + tail


def scope(value):
    value = value.rstrip("/")
    path = Path(value)
    if not value or path.is_absolute() or ".." in path.parts or any(c in value for c in "|,\n\r"):
        raise ValueError("Scopes must be relative paths without '..', commas or table delimiters")
    return path.as_posix()


def overlaps(a, b):
    return a == b or a.startswith(b + "/") or b.startswith(a + "/") or a == "." or b == "."


def validate_migrations(root):
    dialects = []
    for directory in ("migrations", "migrations_pg"):
        versions = {}
        path = root / "backend/storage" / directory
        for file in sorted(path.glob("*.sql")):
            match = re.fullmatch(r"(\d+)_(.+)\.sql", file.name)
            if not match:
                raise ValueError(f"Invalid migration filename: {file}")
            version = int(match[1])
            if version in versions:
                raise ValueError(f"Duplicate migration version {version}: {versions[version]} and {file.name}")
            versions[version] = file.name
        dialects.append(versions)
    if dialects[0] != dialects[1]:
        raise ValueError("SQLite and PostgreSQL migration versions/names differ")
    return dialects[0]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["claim", "heartbeat", "release", "check", "migrations"])
    parser.add_argument("--repo", default=".")
    parser.add_argument("--agent")
    parser.add_argument("--feature")
    parser.add_argument("--worktree")
    parser.add_argument("--scope", action="append", default=[])
    parser.add_argument("--migration", type=int)
    parser.add_argument("--token")
    args = parser.parse_args()
    root = shared_root(Path(args.repo).resolve())
    if args.command != "migrations":
        raise ValueError(
            "Legacy ownership commands are disabled. "
            "Use the Go ORCHESTRATOR as the sole coordination authority."
        )
    if args.command == "migrations":
        # Validate this checkout, rather than the shared root, for PR CI.
        checkout = Path(git(args.repo, "rev-parse", "--show-toplevel"))
        print(json.dumps({"migrations": validate_migrations(checkout), "status": "ok"}))
        return
    meeting = root / "multi-agent-orchestrator/agentmeeting.md"
    with locked(root):
        text = meeting.read_text()
        claims = rows(text)
        now = dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds")
        if args.command == "claim":
            if not args.agent or not args.feature or not args.worktree or not args.scope:
                raise ValueError("claim requires --agent, --feature, --worktree and --scope")
            if any(c in args.agent + args.feature for c in "|\n\r"):
                raise ValueError("Invalid agent/feature")
            # Resolve numeric backlog aliases to the roadmap feature ID.
            for line in text.splitlines():
                cells = [part.strip() for part in line.strip("|").split("|")]
                if len(cells) != 5:
                    continue
                identifier = cells[1].split(":", 1)[0]
                if re.fullmatch(r"P\d+(?:\.\d+[a-z]?)?", identifier):
                    if args.feature.lstrip("#") == cells[0] or args.feature.casefold() == identifier.casefold():
                        args.feature = identifier
                        break
            worktree = Path(args.worktree).resolve()
            if (worktree == root or shared_root(worktree) != root
                    or Path(git(worktree, "rev-parse", "--show-toplevel")).resolve() != worktree):
                raise ValueError("Product claims require an isolated worktree of this repository")
            scopes = [scope(s) for s in args.scope]
            for row in claims:
                if args.migration is not None and row[5] == str(args.migration):
                    raise ValueError(f"Migration {args.migration} already reserved by {row[1]} for {row[0]}")
                if row[6] == "active" and (row[0] == args.feature or any(
                        overlaps(a, b) for a in scopes for b in row[4].split(","))):
                    raise ValueError(f"Claim conflicts with {row[1]}: {row[0]} ({row[4]})")
            # Honor pre-tool backlog claims, too. An existing owner must release/handover explicitly.
            for line in text.splitlines():
                if not line.startswith("| "):
                    continue
                cells = [s.strip() for s in line.strip("|").split("|")]
                if len(cells) == 5 and (cells[0] == args.feature or cells[1].startswith(args.feature + ":")):
                    if cells[2] in ("Claimed", "In Progress", "Blocked", "Done-waiting") and cells[3] and cells[3] != args.agent:
                        raise ValueError(f"Backlog already owned by {cells[3]}: {cells[1]}")
            if args.migration is not None:
                if args.migration <= 0:
                    raise ValueError("Migration version must be positive")
                for checkout in {root, worktree}:
                    if args.migration in validate_migrations(checkout):
                        raise ValueError(f"Migration {args.migration} already exists in {checkout}")
            token = uuid.uuid4().hex
            claims.append([args.feature, args.agent, token, str(worktree), ",".join(scopes),
                           str(args.migration) if args.migration is not None else "-", "active", now])
            atomic_write(meeting, write_rows(text, claims))
            print(json.dumps({"token": token, "feature": args.feature, "status": "active"}))
        else:
            matches = [r for r in claims if r[2] == args.token and r[1] == args.agent and r[6] == "active"]
            if len(matches) != 1:
                raise ValueError("An active claim token and its agent are required")
            row = matches[0]
            if args.command == "check":
                if args.worktree and Path(args.worktree).resolve() != Path(row[3]):
                    raise ValueError("Claim belongs to a different worktree")
                if any(not any(overlaps(scope(s), owner) and (scope(s) == owner or scope(s).startswith(owner + "/") or owner == ".")
                               for owner in row[4].split(",")) for s in args.scope):
                    raise ValueError("Requested file is outside this claim")
            else:
                row[7] = now
                if args.command == "release":
                    row[6] = "released"
                atomic_write(meeting, write_rows(text, claims))
            print(json.dumps({"feature": row[0], "status": row[6], "heartbeat": row[7]}))


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        raise SystemExit(str(error))
