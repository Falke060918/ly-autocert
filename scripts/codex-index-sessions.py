#!/usr/bin/env python3
"""Codex rollout JSONL -> local SQLite FTS5. Index only this checkout unless --all is explicit."""

import argparse
import json
import os
import re
import sqlite3
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
DB_PATH = REPO_ROOT / ".codex" / "index" / "sessions.db"
CODEX_DIR = Path(os.environ.get("CODEX_HOME", str(Path.home() / ".codex")))
PROJECTS_DIR = CODEX_DIR / "sessions"

# 인덱싱할 레코드 타입. thinking/tool_result/attachment 등은 부피 대비 가치가 낮아 제외.
INDEXED_ROLES = ("user", "assistant")


def metadata(path: Path) -> dict:
    with path.open(encoding="utf-8", errors="replace") as handle:
        for line in handle:
            try:
                record = json.loads(line)
            except ValueError:
                continue
            if isinstance(record, dict) and record.get("type") == "session_meta":
                payload = record.get("payload", {})
                return payload if isinstance(payload, dict) else {}
    return {}


def normalize_record(record: dict) -> dict:
    if not isinstance(record, dict) or record.get("type") != "response_item":
        return {}
    payload = record.get("payload")
    if not isinstance(payload, dict) or payload.get("type") != "message":
        return {}
    role = payload.get("role")
    if role not in INDEXED_ROLES:
        return {}
    parts = [b.get("text", "") for b in payload.get("content", [])
             if isinstance(b, dict) and b.get("type") in ("input_text", "output_text", "text")
             and isinstance(b.get("text"), str)]
    return {"type": role, "timestamp": record.get("timestamp", ""),
            "message": {"content": "\n".join(parts)}}


def belongs_to(meta: dict, project: Path) -> bool:
    cwd = meta.get("cwd")
    if not isinstance(cwd, str) or not cwd:
        return False
    try:
        Path(cwd).resolve().relative_to(project.resolve())
        return True
    except (ValueError, OSError):
        return False


def open_db() -> sqlite3.Connection:
    DB_PATH.parent.mkdir(parents=True, exist_ok=True)
    con = sqlite3.connect(DB_PATH)
    con.executescript(
        """
        CREATE VIRTUAL TABLE IF NOT EXISTS messages USING fts5(
            text,
            role UNINDEXED,
            ts UNINDEXED,
            session UNINDEXED,
            file UNINDEXED,
            line UNINDEXED,
            branch UNINDEXED,
            sidechain UNINDEXED,
            tokenize='unicode61'
        );
        CREATE TABLE IF NOT EXISTS files(
            path TEXT PRIMARY KEY,
            mtime REAL,
            size INTEGER
        );
        """
    )
    return con


def extract_text(record: dict) -> str:
    """user/assistant 레코드에서 사람이 쓴·모델이 답한 텍스트만 뽑는다."""
    msg = record.get("message")
    if not isinstance(msg, dict):
        return ""
    content = msg.get("content")
    if isinstance(content, str):
        return content.strip()
    parts = []
    if isinstance(content, list):
        for block in content:
            if isinstance(block, dict) and block.get("type") == "text":
                t = block.get("text", "").strip()
                if t:
                    parts.append(t)
    return "\n".join(parts)


def index_file(con: sqlite3.Connection, path: Path) -> int:
    """한 세션 파일을 (재)인덱싱한다. 추가된 메시지 수를 반환."""
    st = path.stat()
    meta = metadata(path)
    row = con.execute(
        "SELECT mtime, size FROM files WHERE path = ?", (str(path),)
    ).fetchone()
    if row and row[0] == st.st_mtime and row[1] == st.st_size:
        return 0  # 변경 없음 — 건너뜀
    con.execute("DELETE FROM messages WHERE file = ?", (str(path),))
    added = 0
    with open(path, encoding="utf-8", errors="replace") as fh:
        for lineno, line in enumerate(fh, 1):
            try:
                rec = json.loads(line)
            except (json.JSONDecodeError, ValueError):
                continue
            rec = normalize_record(rec)
            if rec.get("type") not in INDEXED_ROLES:
                continue
            text = extract_text(rec)
            if not text:
                continue
            con.execute(
                "INSERT INTO messages(text, role, ts, session, file, line, branch, sidechain)"
                " VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
                (
                    text,
                    rec.get("type"),
                    rec.get("timestamp", ""),
                    meta.get("id", path.stem),
                    str(path),
                    lineno,
                    meta.get("git", {}).get("branch", ""),
                    0,
                ),
            )
            added += 1
    con.execute(
        "INSERT OR REPLACE INTO files(path, mtime, size) VALUES (?, ?, ?)",
        (str(path), st.st_mtime, st.st_size),
    )
    return added


def cmd_index(args) -> int:
    candidates = []
    for directory in (PROJECTS_DIR, CODEX_DIR / "archived_sessions"):
        if directory.is_dir():
            candidates.extend(directory.rglob("*.jsonl"))
    project = Path(args.project) if args.project else REPO_ROOT
    selected = []
    for path in sorted(set(candidates)):
        try:
            if args.all or belongs_to(metadata(path), project):
                selected.append(path)
        except OSError:
            continue
    con = open_db()
    changed = added_total = 0
    with con:
        keep = {str(p) for p in selected}
        # Re-indexing one checkout also removes stale/deleted/out-of-scope sessions.
        for (old,) in con.execute("SELECT path FROM files").fetchall():
            if old not in keep:
                con.execute("DELETE FROM messages WHERE file = ?", (old,))
                con.execute("DELETE FROM files WHERE path = ?", (old,))
        for path in selected:
            added = index_file(con, path)
            if added:
                changed += 1
                added_total += added
    total = con.execute("SELECT count(*) FROM messages").fetchone()[0]
    con.close()
    print(f"파일 {len(selected)}개 확인, {changed}개 갱신, 메시지 {added_total}개 추가 (총 {total}개)")
    if not selected:
        print("이 프로젝트의 Codex rollout 기록 없음. 아직 세션을 시작하지 않았을 수 있습니다.")
    return 0


def build_query(q: str, raw: bool) -> str:
    if raw:
        return q
    # 각 단어를 접두 매칭으로: 조사·어미가 붙은 한국어 토큰까지 잡는다.
    terms = [t for t in re.split(r"\s+", q.strip()) if t]
    return " ".join('"' + t.replace('"', '""') + '"*' for t in terms)


def cmd_search(args) -> int:
    if not DB_PATH.exists():
        print("인덱스가 없다. 먼저: python3 scripts/codex-index-sessions.py index", file=sys.stderr)
        return 1
    con = open_db()
    sql = (
        "SELECT snippet(messages, 0, '[', ']', ' … ', 24), role, ts, session, file, line"
        " FROM messages WHERE messages MATCH ?"
    )
    params = [build_query(args.query, args.raw)]
    if args.role:
        sql += " AND role = ?"
        params.append(args.role)
    if args.session:
        sql += " AND session LIKE ?"
        params.append(args.session + "%")
    sql += " ORDER BY rank LIMIT ?"
    params.append(args.limit)
    try:
        rows = con.execute(sql, params).fetchall()
    except sqlite3.OperationalError as e:
        print(f"질의 오류: {e}", file=sys.stderr)
        return 1
    finally:
        con.close()
    if not rows:
        print("결과 없음")
        return 0
    for snip, role, ts, session, file, line in rows:
        one = " ".join(snip.split())
        print(f"[{ts[:16]}] {role} s={session[:8]} {Path(file).name}:{line}")
        print(f"  {one[:300]}")
    print(f"\n({len(rows)}건) 맥락 보기: show --file <경로> --line <줄>")
    return 0


def cmd_show(args) -> int:
    """검색 결과 지점의 앞뒤 user/assistant 메시지를 시간순으로 펼친다."""
    path = Path(args.file)
    if not path.exists():
        print(f"파일 없음: {path}", file=sys.stderr)
        return 1
    msgs = []  # (lineno, role, text)
    with open(path, encoding="utf-8", errors="replace") as fh:
        for lineno, line in enumerate(fh, 1):
            try:
                rec = json.loads(line)
            except (json.JSONDecodeError, ValueError):
                continue
            rec = normalize_record(rec)
            if rec.get("type") not in INDEXED_ROLES:
                continue
            text = extract_text(rec)
            if text:
                msgs.append((lineno, rec.get("type"), rec.get("timestamp", ""), text))
    idx = next((i for i, m in enumerate(msgs) if m[0] >= args.line), None)
    if idx is None:
        print("해당 줄 근처에 메시지 없음", file=sys.stderr)
        return 1
    lo, hi = max(0, idx - args.context), min(len(msgs), idx + args.context + 1)
    for lineno, role, ts, text in msgs[lo:hi]:
        marker = ">>" if lineno == msgs[idx][0] else "  "
        body = text if len(text) <= args.max_chars else text[: args.max_chars] + " …[잘림]"
        print(f"{marker} [{ts[:16]}] {role} (line {lineno})\n{body}\n")
    return 0


def cmd_sessions(args) -> int:
    if not DB_PATH.exists():
        print("인덱스가 없다. 먼저: python3 scripts/codex-index-sessions.py index", file=sys.stderr)
        return 1
    con = open_db()
    rows = con.execute(
        "SELECT session, min(ts), max(ts), count(*), file FROM messages"
        " GROUP BY session ORDER BY max(ts) DESC LIMIT ?",
        (args.limit,),
    ).fetchall()
    con.close()
    for session, first, last, n, file in rows:
        print(f"{session[:8]}  {first[:16]} ~ {last[:16]}  msgs={n}  {Path(file).name}")
    return 0


def main() -> int:
    for stream in (sys.stdout, sys.stderr):
        if hasattr(stream, "reconfigure"):
            stream.reconfigure(encoding="utf-8")
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = p.add_subparsers(dest="cmd", required=True)

    pi = sub.add_parser("index", help="세션 기록 인덱싱 (증분)")
    pi.add_argument("--project", help="프로젝트 경로 (기본: 이 저장소)")
    pi.add_argument("--all", action="store_true", help="모든 프로젝트 인덱싱")
    pi.set_defaults(fn=cmd_index)

    ps = sub.add_parser("search", help="인덱스 검색")
    ps.add_argument("query")
    ps.add_argument("--limit", type=int, default=10)
    ps.add_argument("--role", choices=INDEXED_ROLES)
    ps.add_argument("--session", help="세션 ID(접두 일치)")
    ps.add_argument("--raw", action="store_true", help="FTS5 질의문을 그대로 사용")
    ps.set_defaults(fn=cmd_search)

    pw = sub.add_parser("show", help="검색 지점의 앞뒤 대화 보기")
    pw.add_argument("--file", required=True)
    pw.add_argument("--line", type=int, required=True)
    pw.add_argument("--context", type=int, default=2, help="앞뒤 메시지 수 (기본 2)")
    pw.add_argument("--max-chars", type=int, default=1500)
    pw.set_defaults(fn=cmd_show)

    pl = sub.add_parser("sessions", help="인덱싱된 세션 목록")
    pl.add_argument("--limit", type=int, default=20)
    pl.set_defaults(fn=cmd_sessions)

    args = p.parse_args()
    return args.fn(args)


if __name__ == "__main__":
    sys.exit(main())
