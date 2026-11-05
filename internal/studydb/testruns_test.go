package studydb

import (
	"testing"
	"time"
)

func TestRecordTestRunCountsRunsAndFixtures(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	fail := []FailedCase{{Kind: "edge", Name: "empty input"}, {Kind: "example", Name: "example 1"}}
	if err := db.Owner().RecordTestRun("two-sum", now, 3, 2, fail); err != nil {
		t.Fatal(err)
	}
	if err := db.Owner().RecordTestRun("two-sum", now.Add(time.Minute), 4, 1, fail[:1]); err != nil {
		t.Fatal(err)
	}
	if err := db.Owner().RecordTestRun("two-sum", now.Add(2*time.Minute), 5, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Owner().RecordTestRun("valid-anagram", now, 1, 1, []FailedCase{{Kind: "edge", Name: "single"}}); err != nil {
		t.Fatal(err)
	}

	tally, err := db.Owner().Tally("two-sum")
	if err != nil {
		t.Fatal(err)
	}
	if tally.Runs != 3 || tally.Clean != 1 || tally.Share() != 33 {
		t.Errorf("tally = %+v (%d%%), want 3 runs, 1 clean", tally, tally.Share())
	}
	if all, err := db.Owner().Tally(""); err != nil || all.Runs != 4 {
		t.Errorf("global tally = %+v, %v; want 4 runs", all, err)
	}

	cases, err := db.Owner().CaseFailures("two-sum", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 {
		t.Fatalf("got %d fixtures, want 2", len(cases))
	}
	// Most-failed first: the edge case broke in both failing runs.
	if cases[0].Name != "empty input" || cases[0].Failures != 2 {
		t.Errorf("first = %s/%d, want empty input failing twice", cases[0].Name, cases[0].Failures)
	}
	if cases[1].Failures != 1 {
		t.Errorf("second counted %d, want 1", cases[1].Failures)
	}
	if !cases[0].LastAt.Equal(now.Add(time.Minute)) {
		t.Errorf("LastAt = %v, want the later run", cases[0].LastAt)
	}

	kinds, err := db.Owner().FailuresByKind()
	if err != nil {
		t.Fatal(err)
	}
	if kinds["edge"] != 3 || kinds["example"] != 1 {
		t.Errorf("by kind = %v, want 3 edge and 1 example", kinds)
	}

	if got, err := db.Owner().CaseFailures("", 1); err != nil || len(got) != 1 {
		t.Errorf("limit 1 returned %d, %v", len(got), err)
	}
}

func TestTallyAndFailuresOnAnEmptyDatabase(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tally, err := db.Owner().Tally("")
	if err != nil {
		t.Fatal(err)
	}
	if tally.Runs != 0 || tally.Share() != 0 {
		t.Errorf("tally = %+v, want zeroes without dividing by zero", tally)
	}
	if cases, err := db.Owner().CaseFailures("", 0); err != nil || len(cases) != 0 {
		t.Errorf("cases = %v, %v", cases, err)
	}
	if kinds, err := db.Owner().FailuresByKind(); err != nil || len(kinds) != 0 {
		t.Errorf("kinds = %v, %v", kinds, err)
	}
}
