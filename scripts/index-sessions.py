#!/usr/bin/env python3
"""index-sessions.py — Claude Code 세션 기록(JSONL)을 SQLite FTS5로 인덱싱·검색한다.

design/04-망각-대응.md "과거 세션 검색 (hermes식)" 구현.
/clear 뒤에도 ~/.claude/projects/<slug>/*.jsonl 에 남는 과거 대화를
검색 가능하게 만드는 마지막 안전망이다.

사용법:
  python3 scripts/index-sessions.py index [--project SLUG | --all]
  python3 scripts/index-sessions.py search "질의어" [--limit N] [--role user|assistant] [--session ID]
  python3 scripts/index-sessions.py show --file PATH --line N [--context K]
  python3 scripts/index-sessions.py sessions [--limit N]

- index: 증분 갱신 — 파일의 (mtime, size)가 저장값과 같으면 건너뛰고,
  바뀐 파일만 지우고 다시 인덱싱한다. 기본 대상은 현재 저장소의 프로젝트 슬러그.
- search: FTS5 질의. 각 단어를 접두 매칭(word*)으로 확장한다
  (unicode61 토크나이저는 "폰트"로 "폰트를"을 못 찾으므로).
  구문검색("...")이나 FTS5 연산자를 쓰려면 --raw 를 붙인다.
- show: 검색 결과 한 건의 앞뒤 대화를 펼쳐 본다 (discovery → scroll).

DB는 <repo>/.claude/index/sessions.db — 로컬 전용, gitignore 대상. 커밋 금지.
파싱은 전부 python(json)으로 한다 — bash로 |-구분 레코드를 파싱하지 않는다(v1 실측).
"""

import argparse
import json
import os
import re
import sqlite3
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
DB_PATH = REPO_ROOT / ".claude" / "index" / "sessions.db"
PROJECTS_DIR = Path.home() / ".claude" / "projects"

# 인덱싱할 레코드 타입. thinking/tool_result/attachment 등은 부피 대비 가치가 낮아 제외.
INDEXED_ROLES = ("user", "assistant")


def project_slug(path: Path) -> str:
    """Claude Code가 쓰는 프로젝트 디렉터리 슬러그: 영숫자 외 전부 '-'."""
    return re.sub(r"[^A-Za-z0-9]", "-", str(path))


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
            if not isinstance(rec, dict) or rec.get("type") not in INDEXED_ROLES:
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
                    rec.get("sessionId", path.stem),
                    str(path),
                    lineno,
                    rec.get("gitBranch", ""),
                    1 if rec.get("isSidechain") else 0,
                ),
            )
            added += 1
    con.execute(
        "INSERT OR REPLACE INTO files(path, mtime, size) VALUES (?, ?, ?)",
        (str(path), st.st_mtime, st.st_size),
    )
    return added


def cmd_index(args) -> int:
    if args.all:
        dirs = sorted(d for d in PROJECTS_DIR.iterdir() if d.is_dir())
    else:
        slug = args.project or project_slug(REPO_ROOT)
        target = PROJECTS_DIR / slug
        if not target.is_dir():
            print(f"세션 기록 없음: {target}", file=sys.stderr)
            print("힌트: --all 또는 --project <슬러그> (ls ~/.claude/projects/)", file=sys.stderr)
            return 1
        dirs = [target]
    con = open_db()
    files = changed = added_total = 0
    with con:
        for d in dirs:
            for f in sorted(d.glob("*.jsonl")):
                files += 1
                added = index_file(con, f)
                if added:
                    changed += 1
                    added_total += added
    total = con.execute("SELECT count(*) FROM messages").fetchone()[0]
    print(f"파일 {files}개 확인, {changed}개 갱신, 메시지 {added_total}개 추가 (인덱스 총 {total}개)")
    print(f"DB: {DB_PATH}")
    return 0


def build_query(q: str, raw: bool) -> str:
    if raw:
        return q
    # 각 단어를 접두 매칭으로: 조사·어미가 붙은 한국어 토큰까지 잡는다.
    terms = [t for t in re.split(r"\s+", q.strip()) if t]
    return " ".join(f'"{t}"*' for t in terms)


def cmd_search(args) -> int:
    if not DB_PATH.exists():
        print("인덱스가 없다. 먼저: python3 scripts/index-sessions.py index", file=sys.stderr)
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
            if not isinstance(rec, dict) or rec.get("type") not in INDEXED_ROLES:
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
        print("인덱스가 없다. 먼저: python3 scripts/index-sessions.py index", file=sys.stderr)
        return 1
    con = open_db()
    rows = con.execute(
        "SELECT session, min(ts), max(ts), count(*), file FROM messages"
        " GROUP BY session ORDER BY max(ts) DESC LIMIT ?",
        (args.limit,),
    ).fetchall()
    for session, first, last, n, file in rows:
        print(f"{session[:8]}  {first[:16]} ~ {last[:16]}  msgs={n}  {Path(file).name}")
    return 0


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = p.add_subparsers(dest="cmd", required=True)

    pi = sub.add_parser("index", help="세션 기록 인덱싱 (증분)")
    pi.add_argument("--project", help="프로젝트 슬러그 (기본: 이 저장소)")
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
