package next_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInvalidTypedProgramsDoNotCompile(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(cwd)
	for name, body := range map[string]string{
		"wrong comparison type":           `u:=models.UsersAs("u");_ = u.Name.EQ(123)`,
		"wrong update type":               `_ = models.UsersUpdate{Name:next.Set(123)}`,
		"nullable into nonnullable input": `_ = models.UsersUpdate{Name:next.Null[string]()}`,
		"wrong cursor type":               `u:=models.UsersAs("u");_ = next.Cursor(u.ID,"wrong",false)`,
		"readonly generated column":       `u:=models.UsersAs("u");_ = u.Fingerprint.Set(2)`,
		"wrong full row result":           `u:=models.UsersAs("u");var rows []*models.PostsRow;rows,_=u.Query().AllContext(context.Background());_ = rows`,
		"wrong table insert":              `u:=models.UsersAs("u");_ = u.Insert(models.PostsInsert{})`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			mod := fmt.Sprintf("module example.org/typecheck\n\ngo 1.27.1\nrequire github.com/stephenafamo/bob v0.0.0\nreplace github.com/stephenafamo/bob => %q\n", root)
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
				t.Fatal(err)
			}
			source := `package typecheck
import("context";"github.com/stephenafamo/bob/next";"github.com/stephenafamo/bob/next/examples/models")
var _ = context.Background
var _ = next.Postgres
var _ = models.UsersTable
func invalid(){` + body + "}\n"
			if err := os.WriteFile(filepath.Join(dir, "invalid.go"), []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "test", "-mod=mod", ".")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatal("invalid typed program compiled")
			}
			if ctx.Err() != nil || !strings.Contains(string(output), "invalid.go:") {
				t.Fatalf("failed without a source type error: %s", output)
			}
		})
	}
}
