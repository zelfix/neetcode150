package web

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

// A suggested name is two words and a number, the shape every forum has taught
// people to recognise as "not my real name".
//
// It matters more here than it looks. Until the people page existed nothing on
// this site showed who you were; now a name is visible to everybody who shares
// the instance. Offering a pseudonym by default means nobody has to decide, on
// the spot and with a form in front of them, how much of their real name to
// hand over to a study group.
var (
	nameAdjectives = []string{
		"amber", "brisk", "calm", "candid", "clever", "crisp", "curious", "deft",
		"dogged", "eager", "easy", "fair", "fleet", "fond", "frank", "gentle",
		"glad", "keen", "kind", "lively", "lucid", "mellow", "mild", "nimble",
		"patient", "placid", "plucky", "prime", "quiet", "rapid", "ready", "serene",
		"sharp", "silent", "snug", "spry", "stout", "sunny", "swift", "tidy",
	}
	nameNouns = []string{
		"acorn", "anchor", "aspen", "badger", "beacon", "birch", "bison", "brook",
		"cedar", "comet", "coral", "crane", "delta", "ember", "falcon", "fern",
		"harbor", "heron", "ingot", "jetty", "kestrel", "lantern", "maple", "marlin",
		"meadow", "orchid", "otter", "pebble", "quarry", "raven", "ridge", "sable",
		"sparrow", "spruce", "summit", "thicket", "tundra", "walnut", "willow", "zephyr",
	}
)

// pick is crypto/rand rather than math/rand: a suggested name is shown to other
// people, and a predictable sequence of them would say in what order a group
// joined.
func pick(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		// rand.Reader does not fail in practice, and a worse name is not worth
		// failing a join over.
		return 0
	}
	return int(v.Int64())
}

// suggestName invents one. It is only a suggestion: the form it appears in is
// editable, and nothing stops somebody typing their own name instead.
func suggestName() string {
	return fmt.Sprintf("%s_%s_%d",
		nameAdjectives[pick(len(nameAdjectives))],
		nameNouns[pick(len(nameNouns))],
		100+pick(900))
}

// freeSuggestion tries a few times to land on one nobody has. With 40×40×900
// combinations against a handful of people a first try almost always works;
// the loop is here so that "almost" is not load-bearing.
func (s *Server) freeSuggestion() string {
	users, err := s.db.Users()
	if err != nil {
		return suggestName()
	}
	taken := make(map[string]bool, len(users))
	for _, u := range users {
		taken[strings.ToLower(u.Name)] = true
	}
	for range 8 {
		if name := suggestName(); !taken[strings.ToLower(name)] {
			return name
		}
	}
	return suggestName()
}
