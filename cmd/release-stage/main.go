package main

import (
	"fmt"
	policy "github.com/KazuhaHub/passwall-sub-panel/internal/version"
	"os"
	"strconv"
	"strings"
)

func main() {
	args := os.Args[1:]
	fail := func(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
	if len(args) < 1 {
		fail(fmt.Errorf("expected stage, aliases, newer or advance"))
	}
	switch args[0] {
	case "stage", "aliases":
		if len(args) != 3 {
			fail(fmt.Errorf("expected version and approval/prerelease boolean"))
		}
		flag, err := strconv.ParseBool(args[2])
		if err != nil {
			fail(err)
		}
		if policy.ReleaseStage(args[1], false) == "unknown" {
			fail(fmt.Errorf("invalid version"))
		}
		if args[0] == "stage" {
			fmt.Println(policy.ReleaseStage(args[1], flag))
		} else {
			fmt.Println(strings.Join(policy.ReleaseAliases(args[1], flag), " "))
		}
	case "newer":
		if len(args) != 3 || policy.ReleaseStage(args[1], false) == "unknown" || policy.ReleaseStage(args[2], false) == "unknown" {
			fail(fmt.Errorf("expected two valid versions"))
		}
		fmt.Println(policy.CompareRelease(args[1], args[2]) > 0)
	case "advance":
		if len(args) != 4 {
			fail(fmt.Errorf("expected source tag, target stage and tag-list path"))
		}
		data, err := os.ReadFile(args[3])
		if err != nil {
			fail(err)
		}
		v, err := policy.AdvanceRelease(args[1], args[2], strings.Fields(string(data)))
		if err != nil {
			fail(err)
		}
		fmt.Println("v" + v)
	default:
		fail(fmt.Errorf("unknown operation"))
	}
}
