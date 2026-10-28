# Persistent server and crash logs

Erupe writes to the Docker console and to `logs/erupe.log` by default. Existing
configurations need no new keys. Zap server messages, recovered packet-handler
panics and their stack traces, standard-library log messages, startup failure
messages and HTTP access logs are retained. The existing health-probe access-log
filter is preserved. Direct third-party stderr writes are saved in `crash.log`
when crash capture is enabled; direct stdout writes remain console-only.

```json
"Logging": {
  "Enabled": true,
  "Directory": "logs",
  "MaxSizeMB": 50,
  "MaxBackups": 10,
  "MaxAgeDays": 14,
  "Compress": true,
  "CaptureCrashes": true
}
```

Relative paths resolve from the process working directory (`/app` in Docker).
The directory must be writable by the container user. Erupe opens its files
before starting the database or network listeners, and fails startup with a
console error if the destination is unusable. Logging settings are read before
full config validation so normal startup/config-validation failures are saved.
A malformed config uses the default log destination to record the parse error;
an invalid logging destination/value is reported on the console. Changing
settings requires a restart. `Enabled: false` keeps console logging only.

## Unraid mapping

Add a **Path** with Read/Write access:

| Host Path | Container Path |
| --- | --- |
| `/mnt/user/appdata/erupe/logs` | `/app/logs` |

The bundled Docker Compose example also mounts `./logs:/app/logs`. Create the
host directory with ownership/permissions allowing the image's `erupe` user to
write to it; a Read/Write mapping alone does not override filesystem permissions.
Keep the standard `./erupe-ce` entrypoint: no shell redirection is necessary,
and Unraid's Docker log viewer continues to work.

## Files and retention

- `erupe.log`: current ordinary log. Before a write would exceed `MaxSizeMB`
  MiB, the current file becomes `erupe-<timestamp>.log` (or `.log.gz`). Existing
  current logs are appended after a restart. A single entry larger than the
  limit is rejected by the file sink and remains visible in console output.
- `crash.log`: raw stderr, including unhandled panics in any goroutine, fatal
  runtime errors and direct third-party stderr diagnostics. A small supervisor
  starts the same executable as the game-server child and saves/forwards stderr
  to the Docker console. It waits for stderr EOF before exiting with the child's
  exit status, recording a UTC timestamp, process ID and status for nonzero
  exits. An empty file during healthy operation is normal. Ordinary Zap
  and standard-library logs use stdout so they do not fill the crash report.
- A nonempty `crash.log` is archived as `crash-<timestamp>.log` on the next
  startup, optionally compressed. It also rotates at `MaxSizeMB` while running,
  so repeated stderr diagnostics cannot grow it indefinitely. A large report
  can span archives; retain all of them when investigating a crash.
- Each family independently retains at most `MaxBackups` archives and removes
  archives older than `MaxAgeDays`. Cleanup/compression runs asynchronously on
  opening/rotating files. The active files are not expired. Defaults are ten
  archives per family and fourteen days. Multiple processes must not share a
  log directory; each container needs its own mapping.

Writes to the ordinary log are not buffered in Go. The supervisor forwards
SIGTERM/interrupt to the server so its usual shutdown countdown, draining and
saves still run; it does not restart a crashed server. Docker's restart policy
continues to control restarts. Keep the normal entrypoint and allow enough Docker
stop time for the existing countdown/drain. Windows console control delivery
follows Windows process-group behavior; explicit Unix signal forwarding is for
Linux/Unraid.

The external stderr capture deliberately avoids Go 1.25's
[`SetCrashOutput` fatal-message omission](https://github.com/golang/go/issues/76859).
It preserves the error's first line as well as the stack, including when the
server's own deferred cleanup runs during a panic. Direct stdout writes outside
the configured log writers remain console-only. OS SIGKILL, container OOM kills,
host power loss and failures before logging initializes cannot produce a Go
crash report; investigate Docker state and host kernel logs for those cases. This
is not a core-dump collector or a guarantee against storage hardware failure.
