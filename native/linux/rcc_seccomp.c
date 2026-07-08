// rcc_seccomp.c — Linux seccomp-user-notify interception for remote-cc-adapter.
//
// This is the brain-side interception載体 for Linux (design doc §4.1.3 / §4.2).
// Unlike macOS, Bun on Linux issues raw syscalls that bypass glibc, so
// LD_PRELOAD symbol interposition misses its file I/O; a seccomp filter traps at
// the syscall boundary instead and catches everything.
//
// Model (ported from the validated POC seccomp/sec.c):
//   - fork; the child installs a seccomp filter that turns openat into a
//     USER_NOTIF, hands the listener fd to the parent (supervisor) over a
//     socketpair via SCM_RIGHTS, then execs the target (claude).
//   - the supervisor loops on NOTIF_RECV, reads the path from /proc/<pid>/mem,
//     and for routed paths fetches the file from the adapter over the fs IO-RPC
//     protocol into a memfd, then injects that fd with NOTIF_ADDFD(FLAG_SEND).
//   - non-routed opens get FLAG_CONTINUE (the kernel runs the real openat).
//
// Documented limitation (design doc §4.1.3 step 2): seccomp only traps openat,
// so the injected fd's subsequent read/lseek/close are real syscalls we no
// longer see. This build therefore fetches the WHOLE routed file into the memfd
// up front rather than slicing on demand like the macOS dylib. Lazy slicing on
// Linux needs either trapping read/lseek too or a FUSE backing store; that is
// future work.
//
// Invocation (by the adapter, see internal/adapter/launch.go):
//   rcc_seccomp <target-binary> [args...]
// Environment: RCC_ADAPTER_SOCK, RCC_REMOTE_PREFIXES (as on macOS).

#define _GNU_SOURCE
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <string.h>
#include <unistd.h>
#include <errno.h>
#include <fcntl.h>
#include <sys/prctl.h>
#include <sys/ioctl.h>
#include <sys/syscall.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <sys/wait.h>
#include <sys/mman.h>
#include <linux/seccomp.h>
#include <linux/filter.h>
#include <linux/audit.h>

#ifndef SECCOMP_ADDFD_FLAG_SEND
#define SECCOMP_ADDFD_FLAG_SEND (1UL << 1)
#endif
#ifndef SECCOMP_USER_NOTIF_FLAG_CONTINUE
#define SECCOMP_USER_NOTIF_FLAG_CONTINUE (1UL << 0)
#endif

enum { OP_OPEN = 2, OP_PREAD = 3, OP_CLOSE = 5 };

// ---- seccomp filter --------------------------------------------------------

static int seccomp(unsigned op, unsigned fl, void *a) { return syscall(__NR_seccomp, op, fl, a); }

static int install_filter(void) {
  struct sock_filter f[] = {
      BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, nr)),
      BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_openat, 0, 1),
      BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_USER_NOTIF),
      BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
  };
  struct sock_fprog p = {.len = 4, .filter = f};
  prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0);
  return seccomp(SECCOMP_SET_MODE_FILTER, SECCOMP_FILTER_FLAG_NEW_LISTENER, &p);
}

static int send_fd(int sock, int fd) {
  struct msghdr m = {0};
  char cbuf[CMSG_SPACE(sizeof(int))] = {0};
  struct iovec io = {.iov_base = "x", .iov_len = 1};
  m.msg_iov = &io; m.msg_iovlen = 1; m.msg_control = cbuf; m.msg_controllen = sizeof cbuf;
  struct cmsghdr *c = CMSG_FIRSTHDR(&m);
  c->cmsg_level = SOL_SOCKET; c->cmsg_type = SCM_RIGHTS; c->cmsg_len = CMSG_LEN(sizeof(int));
  memcpy(CMSG_DATA(c), &fd, sizeof(int));
  return sendmsg(sock, &m, 0);
}
static int recv_fd(int sock) {
  struct msghdr m = {0};
  char cbuf[CMSG_SPACE(sizeof(int))] = {0};
  char d; struct iovec io = {.iov_base = &d, .iov_len = 1};
  m.msg_iov = &io; m.msg_iovlen = 1; m.msg_control = cbuf; m.msg_controllen = sizeof cbuf;
  if (recvmsg(sock, &m, 0) < 0) return -1;
  struct cmsghdr *c = CMSG_FIRSTHDR(&m);
  int fd; memcpy(&fd, CMSG_DATA(c), sizeof(int));
  return fd;
}

static char *read_path(pid_t pid, unsigned long addr) {
  static char b[4096];
  char mm[64];
  snprintf(mm, sizeof mm, "/proc/%d/mem", pid);
  int fd = open(mm, O_RDONLY);
  if (fd < 0) return NULL;
  ssize_t n = pread(fd, b, sizeof b - 1, addr);
  close(fd);
  if (n <= 0) return NULL;
  b[n] = 0;
  return b;
}

// ---- routing ---------------------------------------------------------------

static int is_remote(const char *path) {
  if (!path) return 0;
  const char *pre = getenv("RCC_REMOTE_PREFIXES");
  if (!pre || !*pre) return 0;
  char *dup = strdup(pre);
  int hit = 0;
  for (char *tok = strtok(dup, ":"); tok && !hit; tok = strtok(NULL, ":")) {
    size_t l = strlen(tok);
    if (l == 0) continue;
    if (strncmp(path, tok, l) == 0 && (path[l] == '\0' || path[l] == '/')) hit = 1;
  }
  free(dup);
  return hit;
}

// ---- fs IO-RPC client (mirror internal/protocol) ---------------------------

static int writen(int fd, const void *buf, size_t n) {
  const char *p = buf;
  while (n) { ssize_t w = write(fd, p, n); if (w <= 0) return -1; p += w; n -= (size_t)w; }
  return 0;
}
static int readn(int fd, void *buf, size_t n) {
  char *p = buf;
  while (n) { ssize_t r = read(fd, p, n); if (r <= 0) return -1; p += r; n -= (size_t)r; }
  return 0;
}

static int adapter_connect(void) {
  const char *path = getenv("RCC_ADAPTER_SOCK");
  if (!path || !*path) return -1;
  int fd = socket(AF_UNIX, SOCK_STREAM, 0);
  if (fd < 0) return -1;
  struct sockaddr_un sa;
  memset(&sa, 0, sizeof sa);
  sa.sun_family = AF_UNIX;
  strncpy(sa.sun_path, path, sizeof sa.sun_path - 1);
  if (connect(fd, (struct sockaddr *)&sa, sizeof sa) != 0) { close(fd); return -1; }
  return fd;
}

typedef struct { unsigned char *b; size_t len, cap; } buf_t;
static void bput(buf_t *s, const void *p, size_t n) {
  if (s->len + n > s->cap) { while (s->len + n > s->cap) s->cap = s->cap ? s->cap * 2 : 64; s->b = realloc(s->b, s->cap); }
  memcpy(s->b + s->len, p, n); s->len += n;
}
static void bu8(buf_t *s, uint8_t v) { bput(s, &v, 1); }
static void bu32(buf_t *s, uint32_t v) { unsigned char t[4] = {v >> 24, v >> 16, v >> 8, v}; bput(s, t, 4); }
static void bu64(buf_t *s, uint64_t v) { unsigned char t[8] = {v >> 56, v >> 48, v >> 40, v >> 32, v >> 24, v >> 16, v >> 8, v}; bput(s, t, 8); }
static void bstr(buf_t *s, const char *p) { size_t n = strlen(p); bu32(s, (uint32_t)n); bput(s, p, n); }

// One request/response round-trip on conn. Returns malloc'd body + *rlen, or NULL.
static unsigned char *rpc(int conn, const buf_t *req, size_t *rlen) {
  unsigned char hdr[4] = {req->len >> 24, req->len >> 16, req->len >> 8, req->len};
  if (writen(conn, hdr, 4) || writen(conn, req->b, req->len)) return NULL;
  if (readn(conn, hdr, 4)) return NULL;
  uint32_t n = (hdr[0] << 24) | (hdr[1] << 16) | (hdr[2] << 8) | hdr[3];
  unsigned char *body = malloc(n ? n : 1);
  if (n && readn(conn, body, n)) { free(body); return NULL; }
  *rlen = n;
  return body;
}
static uint32_t rd_u32(const unsigned char *b, size_t *o) { uint32_t v = (b[*o] << 24) | (b[*o + 1] << 16) | (b[*o + 2] << 8) | b[*o + 3]; *o += 4; return v; }
static uint64_t rd_u64(const unsigned char *b, size_t *o) { uint64_t hi = rd_u32(b, o); uint64_t lo = rd_u32(b, o); return (hi << 32) | lo; }

// Fetch a routed file into a fresh memfd. Returns the memfd (offset 0) or -1.
static int fetch_to_memfd(const char *path) {
  int conn = adapter_connect();
  if (conn < 0) return -1;

  buf_t oq = {0}; bu8(&oq, OP_OPEN); bu32(&oq, O_RDONLY); bstr(&oq, path);
  size_t orl; unsigned char *orb = rpc(conn, &oq, &orl); free(oq.b);
  if (!orb) { close(conn); return -1; }
  size_t o = 0; int32_t err = (int32_t)rd_u32(orb, &o);
  if (err != 0) { free(orb); close(conn); return -1; }
  rd_u32(orb, &o); rd_u64(orb, &o); uint64_t handle = rd_u64(orb, &o); free(orb);

  int mfd = memfd_create("rcc", 0);
  if (mfd < 0) { close(conn); return -1; }

  uint64_t off = 0;
  for (;;) {
    buf_t pq = {0}; bu8(&pq, OP_PREAD); bu64(&pq, handle); bu64(&pq, off); bu32(&pq, 65536);
    size_t prl; unsigned char *prb = rpc(conn, &pq, &prl); free(pq.b);
    if (!prb) break;
    size_t p = 0; if ((int32_t)rd_u32(prb, &p) != 0) { free(prb); break; }
    uint32_t dl = rd_u32(prb, &p);
    if (dl == 0) { free(prb); break; }
    if (writen(mfd, prb + p, dl)) { free(prb); break; }
    off += dl;
    free(prb);
  }

  buf_t cq = {0}; bu8(&cq, OP_CLOSE); bu64(&cq, handle); size_t crl; unsigned char *crb = rpc(conn, &cq, &crl); free(cq.b); free(crb);
  close(conn);

  lseek(mfd, 0, SEEK_SET);
  return mfd;
}

// ---- supervisor loop -------------------------------------------------------

int main(int argc, char **argv) {
  if (argc < 2) { fprintf(stderr, "usage: rcc_seccomp <target> [args...]\n"); return 2; }

  int sk[2];
  if (socketpair(AF_UNIX, SOCK_STREAM, 0, sk) != 0) { perror("socketpair"); return 1; }

  pid_t pid = fork();
  if (pid == 0) {
    close(sk[0]);
    int lf = install_filter();
    if (lf < 0) { perror("seccomp install"); _exit(97); }
    send_fd(sk[1], lf);
    close(lf); close(sk[1]);
    execvp(argv[1], &argv[1]);
    perror("exec");
    _exit(96);
  }
  close(sk[1]);
  int lf = recv_fd(sk[0]);
  if (lf < 0) { fprintf(stderr, "recv listener failed\n"); return 1; }

  struct seccomp_notif *req = calloc(1, sizeof *req + 4096);
  struct seccomp_notif_resp *resp = calloc(1, sizeof *resp + 4096);
  for (;;) {
    memset(req, 0, sizeof *req);
    if (ioctl(lf, SECCOMP_IOCTL_NOTIF_RECV, req)) { if (errno == EINTR) continue; break; }
    char *path = read_path(req->pid, req->data.args[1]);
    if (path && is_remote(path)) {
      int mfd = fetch_to_memfd(path);
      if (mfd >= 0) {
        struct seccomp_notif_addfd af = {0};
        af.id = req->id; af.flags = SECCOMP_ADDFD_FLAG_SEND; af.srcfd = mfd; af.newfd = 0;
        ioctl(lf, SECCOMP_IOCTL_NOTIF_ADDFD, &af);
        close(mfd);
        continue; // ADDFD_FLAG_SEND completes the notification
      }
    }
    memset(resp, 0, sizeof *resp);
    resp->id = req->id;
    resp->flags = SECCOMP_USER_NOTIF_FLAG_CONTINUE;
    ioctl(lf, SECCOMP_IOCTL_NOTIF_SEND, resp);
  }

  int st;
  waitpid(pid, &st, 0);
  return WIFEXITED(st) ? WEXITSTATUS(st) : 1;
}
