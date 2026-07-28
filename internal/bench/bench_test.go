package bench

import (
	"context"
	"testing"

	"github.com/abyssmemes/contextverse/internal/testspace"
)

func armOf(t *testing.T, rep *Report, arm Arm) ArmResult {
	t.Helper()
	for _, a := range rep.Arms {
		if a.Arm == arm {
			return a
		}
	}
	t.Fatalf("no %s arm in the report", arm)
	return ArmResult{}
}

func runSet(t *testing.T, tasks string) *Report {
	t.Helper()
	ts, err := LoadTasks(tasks)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), ts, Options{Root: testspace.Legacy(t)})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// The finding that does not depend on how the questions are worded, and the
// reason this benchmark was worth building: the entry set is a fixed list, so
// a question answered by a document outside it is unreachable no matter how
// many tokens the session spends. Eager delivery is not merely expensive here,
// it is incomplete.
func TestEntrySetCannotReachDocumentsItDoesNotContain(t *testing.T) {
	for _, tasks := range []string{"testdata/literal.yaml", "testdata/paraphrased.yaml"} {
		rep := runSet(t, tasks)
		eager := armOf(t, rep, ArmEntrySet)

		if eager.Reached == eager.Total {
			t.Errorf("%s: the entry set reached every answer; the task set no longer "+
				"exercises documents outside it, so it has stopped testing anything", tasks)
		}
		if eager.Upfront == 0 {
			t.Errorf("%s: the entry set cost nothing, which means it delivered nothing", tasks)
		}
	}
}

// Phrasing changes the map arm's result completely, and that is a fact about
// this benchmark rather than about the design. The retrieval policy here is
// keyword search: it finds a document when the question happens to share words
// with it, and fails when it does not.
//
// This test exists to keep that limitation visible. A favourable number from
// the literal task set is a measurement of how the questions were written —
// the questions were written by someone who already knew the answers — and
// quoting it as evidence that lazy delivery wins would be self-deception.
// Settling that needs a model, which is deliberately not in this suite.
func TestTheMapArmIsSensitiveToPhrasing(t *testing.T) {
	literal := armOf(t, runSet(t, "testdata/literal.yaml"), ArmMap)
	paraphrased := armOf(t, runSet(t, "testdata/paraphrased.yaml"), ArmMap)

	if paraphrased.Reached >= literal.Reached {
		t.Skip("the retrieval policy has become robust to phrasing; if that is " +
			"real rather than an accident of the fixture, this benchmark can " +
			"start making claims it currently cannot")
	}
	t.Logf("keyword retrieval reaches %d/%d answers when the question shares "+
		"vocabulary with the document and %d/%d when it does not — the arm "+
		"measures the policy, not the delivery strategy",
		literal.Reached, literal.Total, paraphrased.Reached, paraphrased.Total)
}

// Paying less to reach nothing is not a saving, and a per-answer figure that
// divides by zero would report it as one.
func TestCostPerAnswerDoesNotFlatterAnArmThatFoundNothing(t *testing.T) {
	a := ArmResult{Upfront: 500, Reached: 0}
	if got := a.TokensPerAnswer(); got != 0 {
		t.Errorf("an arm that reached nothing reports %d tokens per answer", got)
	}
}
