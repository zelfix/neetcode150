package main

import (
	"strings"
	"testing"

	"neetcode150/internal/studydb"
)

// The flags that bound an invite are the whole safety story, so each refusal
// is worth pinning: a default that slipped would be the dangerous one.
func TestInviteRefusesAnUnboundedInvite(t *testing.T) {
	root := t.TempDir()
	cases := map[string][]string{
		"no deadline":      {"new", "--count", "2"},
		"deadline too far": {"new", "--count", "2", "--expires", "999h"},
		"no ceiling":       {"new", "--count", "2", "--expires", "1h", "--budget", "0"},
		"no uses":          {"new", "--count", "0", "--expires", "1h"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if err := cmdInvite(root, args); err == nil {
				t.Errorf("accepted %v", args)
			}
		})
	}

	db, err := studydb.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if invites, err := db.Invites(); err != nil || len(invites) != 0 {
		t.Errorf("a refused invite was still recorded: %v, %v", invites, err)
	}
}

func TestInviteNewPrintsTheLinkOnceAndListsWhatItAllows(t *testing.T) {
	root := t.TempDir()
	out := capture(t, func() {
		if err := cmdInvite(root, []string{"new", "--count", "3", "--expires", "48h",
			"--budget", "7.5", "--note", "study group", "--base-url", "https://study.example.com"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "https://study.example.com/invite/") {
		t.Errorf("no invite link printed:\n%s", out)
	}
	if !strings.Contains(out, "3 uses") || !strings.Contains(out, "$7.50") {
		t.Errorf("the printed bounds are wrong:\n%s", out)
	}
	if !strings.Contains(out, "one time it can be shown") {
		t.Errorf("the output does not say the link cannot be reprinted:\n%s", out)
	}

	// The token is not recoverable from the database: only its hash is there.
	db, err := studydb.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	invites, err := db.Invites()
	if err != nil || len(invites) != 1 {
		t.Fatalf("invites = %v, %v", invites, err)
	}
	token := strings.TrimSpace(strings.SplitN(strings.SplitN(out, "/invite/", 2)[1], "\n", 2)[0])
	if _, err := db.InviteByToken(token, invites[0].CreatedAt); err != nil {
		t.Errorf("the printed token does not resolve: %v", err)
	}

	listed := capture(t, func() {
		if err := cmdInvite(root, []string{"list"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(listed, "study group") || !strings.Contains(listed, "$7.50") {
		t.Errorf("list does not account for the invite:\n%s", listed)
	}
	if strings.Contains(listed, token) {
		t.Error("list printed the token, which is the one thing it must not do")
	}
}

func TestInviteRevokeStopsFurtherUse(t *testing.T) {
	root := t.TempDir()
	out := capture(t, func() {
		if err := cmdInvite(root, []string{"new", "--count", "5", "--expires", "1h"}); err != nil {
			t.Fatal(err)
		}
	})
	token := strings.TrimSpace(strings.SplitN(strings.SplitN(out, "/invite/", 2)[1], "\n", 2)[0])

	db, err := studydb.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	invites, err := db.Invites()
	if err != nil || len(invites) != 1 {
		t.Fatalf("invites = %v, %v", invites, err)
	}
	created := invites[0].CreatedAt
	db.Close()

	capture(t, func() {
		if err := cmdInvite(root, []string{"revoke", "1"}); err != nil {
			t.Fatal(err)
		}
	})

	db2, err := studydb.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if _, err := db2.InviteByToken(token, created); err == nil {
		t.Error("a revoked invite still resolves")
	}
	if err := cmdInvite(root, []string{"revoke", "404"}); err == nil {
		t.Error("revoking an invite that does not exist was accepted")
	}
}
