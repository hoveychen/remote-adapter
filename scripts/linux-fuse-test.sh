#!/usr/bin/env bash
# linux-fuse-test.sh — verify Linux lazy slicing end to end. Runs INSIDE a
# privileged Linux container with /dev/fuse:
#
#   docker run --rm --privileged --device /dev/fuse \
#     -v "$PWD":/src -w /src golang:1.25 scripts/linux-fuse-test.sh
#
# Topology mirrors production: the *run* host has an empty placeholder directory
# at the routed path ($STORE), while the *serve* host has the real project
# there. One container fakes both by giving the executor its own mount namespace
# in which $REAL is bind-mounted onto $STORE. Everything else — adapter, FUSE
# daemon, seccomp supervisor, consumers — sees $STORE as it really is on disk.
#
# Pipeline: rca serve (fs IO-RPC server, backs a real 10 MiB file) <- rca _fuse
# (mounts that remote directory at its own absolute path) <- seccomp supervisor
# <- raw-syscall consumers that read, enumerate, cross-check and mutate.
set -euo pipefail

command -v fusermount3 >/dev/null 2>&1 || { apt-get update -qq >/dev/null && apt-get install -y -qq fuse3 >/dev/null; }

WORK="$(mktemp -d)"
REAL="$WORK/real"; mkdir -p "$REAL"   # the serve host's real project directory
STORE="$WORK/store"; mkdir -p "$STORE" # the run host's placeholder at the routed path
EXECSOCK="$WORK/exec.sock"   # remote executor
ADSOCK="$WORK/ad.sock"       # brain adapter (fs-RPC, raw protocol)
BIG="$STORE/bigfile.dat"     # the path consumers ask for; content lives in $REAL
MARK="LAZY-SLICE-MARKER-9931-XY"  # 25 bytes

echo "== build =="
go build -o "$WORK/rca" ./cmd/rca
cc -O2 -Wall -Wextra -o "$WORK/sup" native/linux/rcc_seccomp.c

echo "== stage 10MiB file with a marker at offset 5MiB (on the serve side) =="
dd if=/dev/zero of="$REAL/bigfile.dat" bs=1M count=10 status=none
printf '%s' "$MARK" | dd of="$REAL/bigfile.dat" bs=1 seek=$((5*1024*1024)) conv=notrunc status=none

echo "== stage a routed directory (openat O_DIRECTORY + getdents64 must work) =="
DIR="$STORE/listme"; mkdir -p "$REAL/listme/nested"
: > "$REAL/listme/alpha.txt"; : > "$REAL/listme/beta.txt"

echo "== start executor in its own mount ns: \$REAL bind-mounted onto \$STORE =="
# The executor resolves routed paths against $STORE and finds the real content;
# no other process in this container does. That is exactly the production split:
# the run host's $STORE is empty, the serve host's $STORE is the project.
unshare -m -- sh -c "mount --bind '$REAL' '$STORE'; exec '$WORK/rca' serve --sock '$EXECSOCK'" >"$WORK/exec.log" 2>&1 &
EXEC_PID=$!
for _ in $(seq 1 50); do [[ -S "$EXECSOCK" ]] && break; sleep 0.1; done

echo "== start adapter (brain, routes STORE -> executor, serves fs-RPC) =="
"$WORK/rca" --serve-fs-only --sock "$EXECSOCK" --adapter-sock "$ADSOCK" --remote-prefix "$STORE" >"$WORK/adapter.log" 2>&1 &
AD_PID=$!
for _ in $(seq 1 50); do [[ -S "$ADSOCK" ]] && break; sleep 0.1; done

echo "== start rca _fuse: mount the remote directory AT its own absolute path =="
# The mount point and the remote root are the same absolute path, so openat,
# stat, statx, getdents64 and getcwd all resolve through one filesystem. The old
# shape — a flat hex(path) namespace that only openat knew how to reach — is what
# gave the target a split view of its own cwd.
"$WORK/rca" _fuse -mount "$STORE" -root "$STORE" -adapter-sock "$ADSOCK" >"$WORK/fuse.log" 2>&1 &
FUSE_PID=$!
for _ in $(seq 1 50); do mountpoint -q "$STORE" 2>/dev/null && break; sleep 0.1; done
mountpoint -q "$STORE" || { echo "FATAL: routed mount never came up"; cat "$WORK/fuse.log"; exit 1; }

echo "== raw consumer: openat routed path, pread 25 bytes @ 5MiB =="
cat > "$WORK/consumer.c" <<'EOF'
#define _GNU_SOURCE
#include <fcntl.h>
#include <unistd.h>
#include <stdio.h>
#include <sys/syscall.h>
int main(int argc, char **argv) {
  (void)argc;
  int fd = syscall(SYS_openat, AT_FDCWD, argv[1], O_RDONLY, 0);
  if (fd < 0) { perror("openat"); return 1; }
  char b[64] = {0};
  ssize_t n = pread(fd, b, 25, 5L*1024*1024);
  if (n < 0) { perror("pread"); return 1; }
  printf("GOT[%zd]:%.*s\n", n, (int)n, b);
  return 0;
}
EOF
cc -O2 -o "$WORK/consumer" "$WORK/consumer.c"

OUT="$(RCC_REMOTE_PREFIXES="$STORE" "$WORK/sup" "$WORK/consumer" "$BIG" 2>"$WORK/sup.log" || true)"
echo "consumer said: $OUT"

echo "== raw consumer: openat routed DIRECTORY, getdents64 the entries =="
cat > "$WORK/dirconsumer.c" <<'EOF'
#define _GNU_SOURCE
#include <fcntl.h>
#include <unistd.h>
#include <stdio.h>
#include <string.h>
#include <sys/syscall.h>
struct linux_dirent64 {
  unsigned long long d_ino, d_off;
  unsigned short d_reclen;
  unsigned char d_type;
  char d_name[];
};
int main(int argc, char **argv) {
  (void)argc;
  int fd = syscall(SYS_openat, AT_FDCWD, argv[1], O_RDONLY | O_DIRECTORY, 0);
  if (fd < 0) { perror("openat"); return 1; }
  char buf[8192];
  for (;;) {
    long n = syscall(SYS_getdents64, fd, buf, sizeof buf);
    if (n < 0) { perror("getdents64"); return 1; }
    if (n == 0) break;
    for (long o = 0; o < n;) {
      struct linux_dirent64 *d = (struct linux_dirent64 *)(buf + o);
      printf("ENT:%s type=%d\n", d->d_name, d->d_type);
      o += d->d_reclen;
    }
  }
  return 0;
}
EOF
cc -O2 -o "$WORK/dirconsumer" "$WORK/dirconsumer.c"

DIROUT="$(RCC_REMOTE_PREFIXES="$STORE" "$WORK/sup" "$WORK/dirconsumer" "$DIR" 2>&1 || true)"
echo "dirconsumer said: $DIROUT"

echo "== raw consumer: openat and stat must agree on the same routed path =="
# The split-view bug: seccomp traps openat but not stat/statx, so a process sees
# its routed cwd through two different filesystems at once. openat lands on the
# FUSE-backed remote file; stat(2) falls through to the (empty) local directory
# and returns ENOENT. bun cross-checks the two and wedges at startup.
cat > "$WORK/splitview.c" <<'EOF'
#define _GNU_SOURCE
#include <fcntl.h>
#include <unistd.h>
#include <stdio.h>
#include <errno.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/statfs.h>
#include <sys/syscall.h>
int main(int argc, char **argv) {
  (void)argc;
  const char *p = argv[1];
  int fd = syscall(SYS_openat, AT_FDCWD, p, O_RDONLY, 0);
  printf("OPENAT:%s\n", fd < 0 ? strerror(errno) : "ok");
  struct stat sb;
  int sr = stat(p, &sb);
  printf("STAT:%s\n", sr < 0 ? strerror(errno) : "ok");
  if (fd < 0 || sr < 0) return 1;
  struct stat fb;
  if (fstat(fd, &fb) < 0) { perror("fstat"); return 1; }
  printf("SIZE_FSTAT:%lld SIZE_STAT:%lld\n", (long long)fb.st_size, (long long)sb.st_size);
  // st_dev pins it down: not merely "both are FUSE", but the very same mount.
  printf("DEV_FSTAT:%llu DEV_STAT:%llu\n",
         (unsigned long long)fb.st_dev, (unsigned long long)sb.st_dev);
  struct statfs ffs, pfs;
  if (fstatfs(fd, &ffs) < 0 || statfs(p, &pfs) < 0) { perror("statfs"); return 1; }
  printf("FSTYPE_FD:0x%lx FSTYPE_PATH:0x%lx\n",
         (unsigned long)ffs.f_type, (unsigned long)pfs.f_type);
  return 0;
}
EOF
cc -O2 -o "$WORK/splitview" "$WORK/splitview.c"

SPLITOUT="$(RCC_REMOTE_PREFIXES="$STORE" "$WORK/sup" "$WORK/splitview" "$BIG" 2>&1 || true)"
echo "splitview said: $SPLITOUT"

echo "== raw consumer: a routed cwd must enumerate and resolve relative names =="
# What claude actually does: chdir into the project, then openat(\".\") +
# getdents64 and stat relative names. Routing is a literal prefix match on the
# path string, so \".\" and \"bigfile.dat\" never match a remote prefix and fall
# through to the empty local directory.
cat > "$WORK/cwdconsumer.c" <<'EOF'
#define _GNU_SOURCE
#include <fcntl.h>
#include <unistd.h>
#include <stdio.h>
#include <errno.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/syscall.h>
struct linux_dirent64 {
  unsigned long long d_ino, d_off;
  unsigned short d_reclen;
  unsigned char d_type;
  char d_name[];
};
int main(int argc, char **argv) {
  (void)argc;
  char cwd[4096];
  if (!getcwd(cwd, sizeof cwd)) { perror("getcwd"); return 1; }
  printf("CWD:%s\n", cwd);
  int fd = syscall(SYS_openat, AT_FDCWD, ".", O_RDONLY | O_DIRECTORY, 0);
  if (fd < 0) { printf("OPENDOT:%s\n", strerror(errno)); return 1; }
  printf("OPENDOT:ok\n");
  char buf[8192];
  for (;;) {
    long n = syscall(SYS_getdents64, fd, buf, sizeof buf);
    if (n < 0) { perror("getdents64"); return 1; }
    if (n == 0) break;
    for (long o = 0; o < n;) {
      struct linux_dirent64 *d = (struct linux_dirent64 *)(buf + o);
      printf("CWDENT:%s\n", d->d_name);
      o += d->d_reclen;
    }
  }
  struct stat sb;
  printf("STATREL:%s\n", stat("bigfile.dat", &sb) < 0 ? strerror(errno) : "ok");
  return 0;
}
EOF
cc -O2 -o "$WORK/cwdconsumer" "$WORK/cwdconsumer.c"

CWDOUT="$(cd "$STORE" && RCC_REMOTE_PREFIXES="$STORE" "$WORK/sup" "$WORK/cwdconsumer" 2>&1 || true)"
echo "cwdconsumer said: $CWDOUT"

echo "== mutate the routed directory: create, write, truncate, mkdir, rename, remove =="
# Exercises the FUSE write path (Create/Write/Setattr/Mkdir/Rename/Unlink/Rmdir)
# with ordinary shell tools. Everything must land on the *serve* side, i.e. in
# $REAL, which only the executor's mount namespace maps onto $STORE.
WROUT=""
write_probe() { WROUT="$WROUT $1"; }
( echo "hello remote" > "$STORE/newfile.txt" ) 2>>"$WORK/write.err" && write_probe "create=ok" || write_probe "create=FAIL"
[[ "$(cat "$REAL/newfile.txt" 2>/dev/null)" == "hello remote" ]] && write_probe "landed=ok" || write_probe "landed=FAIL"
( printf 'xx' > "$STORE/newfile.txt" ) 2>>"$WORK/write.err" && write_probe "trunc=ok" || write_probe "trunc=FAIL"
[[ "$(cat "$REAL/newfile.txt" 2>/dev/null)" == "xx" ]] && write_probe "truncbody=ok" || write_probe "truncbody=FAIL"
mkdir "$STORE/newdir" 2>>"$WORK/write.err" && write_probe "mkdir=ok" || write_probe "mkdir=FAIL"
mv "$STORE/newfile.txt" "$STORE/newdir/moved.txt" 2>>"$WORK/write.err" && write_probe "rename=ok" || write_probe "rename=FAIL"
[[ -f "$REAL/newdir/moved.txt" ]] && write_probe "renamelanded=ok" || write_probe "renamelanded=FAIL"
rm "$STORE/newdir/moved.txt" 2>>"$WORK/write.err" && write_probe "unlink=ok" || write_probe "unlink=FAIL"
rmdir "$STORE/newdir" 2>>"$WORK/write.err" && write_probe "rmdir=ok" || write_probe "rmdir=FAIL"
[[ ! -e "$REAL/newdir" ]] && write_probe "removed=ok" || write_probe "removed=FAIL"
echo "write probes:$WROUT"

fusermount3 -u "$STORE" 2>/dev/null || umount "$STORE" 2>/dev/null || true
kill "$FUSE_PID" 2>/dev/null || true
kill "$AD_PID" 2>/dev/null || true
kill "$EXEC_PID" 2>/dev/null || true

echo
echo "===== VERDICT ====="
PASS=1
if echo "$OUT" | grep -q "$MARK"; then
  echo "[PASS] consumer read the correct slice through the routed mount"
else
  echo "[FAIL] consumer did not read the expected marker"; PASS=0
fi
# Sum bytes returned by PREAD in the fs-RPC server log; must be << 10 MiB.
FETCHED=$(grep -oE '\[fs\] PREAD .* -> [0-9]+' "$WORK/exec.log" | grep -oE '[0-9]+$' | awk '{s+=$1} END{print s+0}')
echo "fs-RPC bytes fetched: ${FETCHED} (file is $((10*1024*1024)))"
if [[ "$FETCHED" -gt 0 && "$FETCHED" -lt $((1024*1024)) ]]; then
  echo "[PASS] lazy: fetched ${FETCHED} bytes, far less than the 10MiB file"
else
  echo "[FAIL] not lazy (fetched ${FETCHED} bytes)"; PASS=0
fi
# A routed directory must open as a directory and enumerate. Regression guard for
# the FUSE layer typing every routed path S_IFREG, which made openat+getdents64
# on a routed cwd fail ENOTDIR and wedged bun's opendir() at startup.
if echo "$DIROUT" | grep -q "ENT:alpha.txt" && echo "$DIROUT" | grep -q "ENT:beta.txt"; then
  echo "[PASS] routed directory enumerated via getdents64"
else
  echo "[FAIL] routed directory did not enumerate: $DIROUT"; PASS=0
fi
if echo "$DIROUT" | grep -q "ENT:nested type=4"; then
  echo "[PASS] nested subdirectory reported d_type=DT_DIR"
else
  echo "[FAIL] nested subdirectory d_type wrong (ripgrep would not recurse)"; PASS=0
fi
# One consistent view: whatever openat resolves to, stat must resolve to as well
# — same filesystem, same size. Anything else is the split view that deadlocks
# bun on a remote-routed cwd.
SV_FD_FS="$(echo "$SPLITOUT" | sed -n 's/.*FSTYPE_FD:\([^ ]*\).*/\1/p')"
SV_PATH_FS="$(echo "$SPLITOUT" | sed -n 's/.*FSTYPE_PATH:\(.*\)/\1/p')"
SV_FSTAT="$(echo "$SPLITOUT" | sed -n 's/SIZE_FSTAT:\([0-9]*\).*/\1/p')"
SV_STAT="$(echo "$SPLITOUT" | sed -n 's/.*SIZE_STAT:\([0-9]*\).*/\1/p')"
SV_FDEV="$(echo "$SPLITOUT" | sed -n 's/DEV_FSTAT:\([0-9]*\).*/\1/p')"
SV_PDEV="$(echo "$SPLITOUT" | sed -n 's/.*DEV_STAT:\([0-9]*\).*/\1/p')"
if echo "$SPLITOUT" | grep -q '^OPENAT:ok' && echo "$SPLITOUT" | grep -q '^STAT:ok' &&
   [[ -n "$SV_FSTAT" && "$SV_FSTAT" == "$SV_STAT" && -n "$SV_FD_FS" && "$SV_FD_FS" == "$SV_PATH_FS" &&
      -n "$SV_FDEV" && "$SV_FDEV" == "$SV_PDEV" ]]; then
  echo "[PASS] openat and stat agree on the routed path (same mount, same size)"
else
  echo "[FAIL] split view on the routed path: $(echo "$SPLITOUT" | tr '\n' ' ')"; PASS=0
fi
# The routed cwd itself must behave like the remote directory: enumerate its
# entries and resolve relative names.
if echo "$CWDOUT" | grep -q "^CWD:$STORE$" && echo "$CWDOUT" | grep -q "^CWDENT:bigfile.dat$" &&
   echo "$CWDOUT" | grep -q "^STATREL:ok$"; then
  echo "[PASS] routed cwd enumerates and resolves relative names"
else
  echo "[FAIL] routed cwd is not the remote directory: $(echo "$CWDOUT" | tr '\n' ' ')"; PASS=0
fi
# Writes must reach the serve host, not a local shadow copy.
if [[ "$WROUT" != *FAIL* ]]; then
  echo "[PASS] routed directory is writable end to end:$WROUT"
else
  echo "[FAIL] routed write path:$WROUT"; PASS=0
  [[ -s "$WORK/write.err" ]] && sed 's/^/       /' "$WORK/write.err"
fi
echo "==================="
rm -rf "$WORK"
exit $(( PASS ? 0 : 1 ))
