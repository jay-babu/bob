// Package pgtest starts a disposable PostgreSQL container with a private Unix
// socket. It never reads application credentials or contacts an existing server.
package pgtest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
)

func Open(t testing.TB) *sql.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("PostgreSQL integration excluded by short mode")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		unavailable(t, "Docker is required for PostgreSQL integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	base, err := os.MkdirTemp("", "bob-next-postgres-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	if err = os.Chmod(base, 0o777); err != nil {
		t.Fatal(err)
	}
	image := os.Getenv("BOB_NEXT_POSTGRES_IMAGE")
	if image == "" {
		image = "postgres:17-bookworm"
	}
	run := func(ctx context.Context, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "docker", append([]string{"--host=unix:///var/run/docker.sock"}, args...)...)
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "DOCKER_HOST=") && !strings.HasPrefix(e, "DOCKER_CONTEXT=") && !strings.HasPrefix(e, "DOCKER_TLS=") && !strings.HasPrefix(e, "DOCKER_TLS_VERIFY=") && !strings.HasPrefix(e, "DOCKER_CERT_PATH=") {
				cmd.Env = append(cmd.Env, e)
			}
		}
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if _, err = run(ctx, "image", "inspect", image, "--format", "{{.Id}}"); err != nil {
		unavailable(t, "cached PostgreSQL test image is unavailable")
	}
	id, err := run(ctx, "run", "--pull=never", "--network=none", "--rm", "-d", "--mount", "type=bind,src="+base+",dst=/var/run/postgresql", "-e", "POSTGRES_HOST_AUTH_METHOD=trust", image, "-c", "listen_addresses=", "-c", "fsync=off", "-c", "shared_buffers=16MB", "-c", "max_connections=20")
	if err != nil {
		t.Fatalf("start isolated PostgreSQL: %s", id)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if out, err := run(cleanup, "rm", "-f", id); err != nil {
			t.Errorf("remove test PostgreSQL: %s", out)
		}
	})
	db, err := sql.Open("pgx", fmt.Sprintf("host=%s user=postgres dbname=postgres sslmode=disable", base))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for {
		// The image's bootstrap server also accepts socket connections, then
		// shuts down. Wait for the final postmaster to replace the entrypoint.
		process, readyErr := run(ctx, "exec", id, "cat", "/proc/1/comm")
		if readyErr == nil && process == "postgres" {
			err = db.PingContext(ctx)
		} else {
			err = fmt.Errorf("PostgreSQL initialization is in progress")
		}
		if err == nil {
			return db
		}
		if ctx.Err() != nil {
			logs, _ := run(context.Background(), "logs", id)
			t.Fatalf("test PostgreSQL did not become ready: %s", logs)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func unavailable(t testing.TB, message string) {
	t.Helper()
	if os.Getenv("BOB_NEXT_REQUIRE_POSTGRES") == "1" {
		t.Fatal(message)
	}
	t.Skip(message)
}

func DSN(t testing.TB, db *sql.DB) string {
	t.Helper()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var dsn string
	if err = conn.Raw(func(value any) error { dsn = value.(*stdlib.Conn).Conn().Config().ConnString(); return nil }); err != nil {
		t.Fatal(err)
	}
	return dsn
}
