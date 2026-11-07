#!/usr/bin/env bash
# coverage-gate.sh — 按包校验单元测试覆盖率。
#
# 用法:
#   scripts/coverage-gate.sh <coverprofile> [--global N] [--exclude RE] [--thresholds FILE]
#
# 阈值文件格式（默认 scripts/coverage-thresholds.txt）:
#   每行 "<包路径> <最低覆盖率>"，包路径相对模块根，"(root)" 表示模块根；# 开头为注释。
#
# 退出码: 0 = 全部达标; 1 = 有包或全局低于阈值; 2 = 用法错误。
set -euo pipefail

PROFILE="${1:-coverage.out}"
shift || true

GLOBAL_MIN=""
EXCLUDE="(^|/)pb(/|$)"
THRESH_FILE="$(cd "$(dirname "$0")" && pwd)/coverage-thresholds.txt"

while [ $# -gt 0 ]; do
  case "$1" in
    --global)     GLOBAL_MIN="${2:?--global 需要数值}"; shift 2 ;;
    --exclude)    EXCLUDE="${2:?--exclude 需要正则}"; shift 2 ;;
    --thresholds) THRESH_FILE="${2:?--thresholds 需要文件}"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

[ -f "$PROFILE" ] || { echo "coverprofile not found: $PROFILE" >&2; exit 2; }
[ -f go.mod ]     || { echo "must run from module root (go.mod not found)" >&2; exit 2; }
[ -f "$THRESH_FILE" ] || { echo "thresholds file not found: $THRESH_FILE" >&2; exit 2; }

MODULE="$(awk '/^module[[:space:]]/{print $2; exit}' go.mod)"

awk -v mod="$MODULE" -v thr="$THRESH_FILE" -v gmin="$GLOBAL_MIN" -v excl="$EXCLUDE" '
BEGIN {
  while ((getline line < thr) > 0) {
    sub(/#.*/, "", line)
    gsub(/^[ \t]+|[ \t]+$/, "", line)
    if (line == "") continue
    n = split(line, a, /[ \t]+/)
    if (n >= 2) min[a[1]] = a[2] + 0
  }
  close(thr)
  failed = 0
}
/^mode:/ { next }
{
  # 行格式: <module>/<pkg>/<file>.go:<start>,<end> <numStmts> <count>
  split($0, parts, ":")
  path = parts[1]
  if (index(path, mod "/") == 1) {
    rel = substr(path, length(mod) + 2)
  } else if (path == mod) {
    rel = "(root)"
  } else {
    next
  }
  if (rel ~ excl) next
  if (match(rel, /\//)) {
    pkg = rel
    sub(/\/[^\/]*$/, "", pkg)
    if (pkg == "") pkg = "(root)"
  } else {
    pkg = "(root)"
  }
  split(parts[2], f, /[ \t]+/)
  stmts = f[2] + 0
  cnt = f[3] + 0
  tot[pkg] += stmts
  if (cnt > 0) cov[pkg] += stmts
  T += stmts
  if (cnt > 0) C += stmts
}
END {
  for (p in tot) {
    if (tot[p] == 0) continue
    pct = 100 * cov[p] / tot[p]
    m = (p in min) ? min[p] : 0
    status = (pct + 0.0001 >= m) ? "ok" : "FAIL"
    if (status == "FAIL") failed = 1
    printf "%-30s %6.1f%%  min %5.1f%%  %s\n", p, pct, m, status
  }
  if (T > 0) printf "%-30s %6.1f%%  (%d/%d statements)\n", "TOTAL", 100 * C / T, C, T
  if (gmin != "" && T > 0 && 100 * C / T + 0.0001 < gmin + 0) {
    printf "FAIL: total %.1f%% below global minimum %.1f%%\n", 100 * C / T, gmin
    failed = 1
  }
  if (failed) {
    print "coverage gate: FAILED"
    exit 1
  }
  print "coverage gate: PASSED"
}
' "$PROFILE"
