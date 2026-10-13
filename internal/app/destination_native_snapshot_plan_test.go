package app

import (
	"errors"
	"regexp"
	"testing"
)

var nativeDestinationTablePattern = regexp.MustCompile(`^dest_[a-z_]+$`)

func nativeDestinationTableIdentifier(kind, name string) (string, error) {
	if !nativeDestinationTablePattern.MatchString(name) {
		return "", errors.New("invalid owned destination table")
	}
	switch kind {
	case "mysql":
		return "`" + name + "`", nil
	case "sqlite", "postgres":
		return `"` + name + `"`, nil
	default:
		return "", errors.New("invalid owned destination dialect")
	}
}

func TestNativeSnapshotIdentifiersRejectForeignTablesAndSQLSyntax(t *testing.T) {
	for kind, want := range map[string]string{"sqlite": `"dest_lists"`, "postgres": `"dest_lists"`, "mysql": "`dest_lists`"} {
		got, err := nativeDestinationTableIdentifier(kind, "dest_lists")
		if err != nil || got != want {
			t.Fatal("owned table identifier lost its actual dialect")
		}
		for _, name := range []string{"users", "dest_", "public.dest_lists", "dest_lists; DROP TABLE users", "dest_lists`", `dest_lists"`, "dest_lists\x00", "dest_私有", "dest_lists2"} {
			if got, err := nativeDestinationTableIdentifier(kind, name); err == nil || got != "" {
				t.Fatal("foreign table or SQL syntax escaped the closed snapshot")
			}
		}
	}
	if got, err := nativeDestinationTableIdentifier("unknown", "dest_lists"); err == nil || got != "" {
		t.Fatal("unknown snapshot dialect silently fell back")
	}
}
