# 79: session names and socket path length

2026-11-05

jobshop changed session ids to `js-s-<id>` (jobshop#615), and a test
there crashed. The session name is used as one path element: the cgroup
dir `<parent>/<session>` and the GC root link `<prefix><session>`. The
host's socket is `<dir>/acp.sock`, and Linux limits a unix socket path to
107 bytes (sun_path is 108 with the NUL). Past that, the host used to fail
at bind, after it had been started.

CheckSpawn now rejects with a PermanentError:

- a session name that is not one path element (`/`, NUL, `.`, `..`) or
  whose GC root link name is over 255 bytes;
- with `WithHostDir(dir)`, a session dir whose `acp.sock` path is over
  MaxSocketPath. The option is variadic so existing callers still build.

The host checks the socket path again before listening, so the error
names the limit either way. Tests in names_test.go cover the jobshop
form, extra dashes, max length and the exact sun_path boundary.
