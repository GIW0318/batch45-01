package task

import (
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"testing"

	impl "gsb/batch45-game-a-01"
)

func TestLocalRegressionDeterministicOutputAcrossGOMAXPROCS(t *testing.T) {
	if os.Getenv("LOCKSTEP_DETERMINISM_HELPER") == "1" {
		t.Skip("helper runs in child processes")
	}

	var baseline string
	for _, procs := range []string{"1", "2", "4"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestLocalRegressionDeterministicOutputHelper$")
		cmd.Env = append(withoutEnv(os.Environ(), "GOMAXPROCS", "LOCKSTEP_DETERMINISM_HELPER"),
			"LOCKSTEP_DETERMINISM_HELPER=1",
			"GOMAXPROCS="+procs,
		)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("GOMAXPROCS=%s helper failed: %v\n%s", procs, err, output)
		}

		result := localRegressionResultLine(string(output))
		if result == "" {
			t.Fatalf("GOMAXPROCS=%s helper produced no result\n%s", procs, output)
		}
		if baseline == "" {
			baseline = result
			continue
		}
		if result != baseline {
			t.Fatalf("GOMAXPROCS=%s produced different output:\nwant %s\ngot  %s", procs, baseline, result)
		}
	}
}

func TestLocalRegressionDeterministicOutputHelper(t *testing.T) {
	if os.Getenv("LOCKSTEP_DETERMINISM_HELPER") != "1" {
		t.Skip("helper for TestLocalRegressionDeterministicOutputAcrossGOMAXPROCS")
	}
	fmt.Printf("LOCKSTEP_RESULT:%s\n", localRegressionScenario())
}

func TestLocalRegressionSortInputsDoesNotMutateInput(t *testing.T) {
	g := impl.NewLockstepGuard(2)
	in := []impl.Input{
		{PlayerID: 1, Frame: 2, Seq: 0, Payload: "b"},
		{PlayerID: 0, Frame: 1, Seq: 0, Payload: "a"},
	}
	original := append([]impl.Input(nil), in...)
	_ = g.SortInputs(in)
	if !reflect.DeepEqual(in, original) {
		t.Fatalf("SortInputs changed its input: %#v", in)
	}
}

func withoutEnv(env []string, names ...string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		keep := true
		for _, name := range names {
			if strings.HasPrefix(entry, name+"=") {
				keep = false
				break
			}
		}
		if keep {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func localRegressionResultLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "LOCKSTEP_RESULT:") {
			return strings.TrimPrefix(line, "LOCKSTEP_RESULT:")
		}
	}
	return ""
}

func localRegressionScenario() string {
	g := impl.NewLockstepGuard(3)
	inputs := []impl.Input{
		{PlayerID: 2, Frame: 12, Seq: 1, Payload: "cast"},
		{PlayerID: 0, Frame: 10, Seq: 2, Payload: "dash"},
		{PlayerID: 1, Frame: 11, Seq: 0, Payload: "move"},
		{PlayerID: 0, Frame: 10, Seq: 1, Payload: "move"},
		{PlayerID: 2, Frame: 10, Seq: 0, Payload: "attack"},
	}

	var out strings.Builder
	for _, input := range g.SortInputs(inputs) {
		fmt.Fprintf(&out, "i:%d/%d/%d/%s;", input.Frame, input.PlayerID, input.Seq, input.Payload)
	}

	permutations := [][]int{
		{0, 1, 2, 3, 4},
		{4, 2, 0, 3, 1},
		{3, 1, 4, 0, 2},
	}
	for _, permutation := range permutations {
		reordered := make([]impl.Input, 0, len(inputs))
		for _, index := range permutation {
			reordered = append(reordered, inputs[index])
		}
		merged := g.MergeFrame(10, reordered)
		players := make([]int, 0, len(merged))
		for player := range merged {
			players = append(players, player)
		}
		sort.Ints(players)
		for _, player := range players {
			input := merged[player]
			fmt.Fprintf(&out, "m:%d/%d/%d/%s;", input.Frame, input.PlayerID, input.Seq, input.Payload)
		}
	}

	entities := map[int]struct{}{40: {}, 10: {}, 30: {}, 20: {}}
	ids := make([]int, 0, len(entities))
	for id := range entities {
		ids = append(ids, id)
	}
	fmt.Fprintf(&out, "e:%v;", g.EntityOrder(ids))
	fmt.Fprintf(&out, "f:%d/%d/%d;", g.Fixed(1.5), g.FixedMul(1500, 2000), g.Precision())
	fmt.Fprintf(&out, "s:%d/%d;", g.Seed("battle-7"), g.CooldownTicks(2, 60))
	fmt.Fprintf(&out, "c:%v;", g.ContiguousFrames([]int{12, 10, 11, 13, 15}))
	fmt.Fprintf(&out, "r:%d/%d;", g.ResumeFrame(145), g.NormalizePrecision(1<<20))
	fmt.Fprintf(&out, "x:%d;", g.DesyncFrame([]uint64{1, 2, 3}, []uint64{1, 9, 3}))
	fmt.Fprintf(&out, "h:%x;", g.RollingChecksum([]uint64{11, 22, 33}))
	return out.String()
}
