package testkit

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	"neetcode150/internal/testbank"
)

// jsonValue rounds a Go value through JSON so that []int and []any, or a
// struct and a map, compare by content. A value JSON cannot carry (NaN, a
// channel) used to come back nil and fail the comparison with no hint why;
// the panic is caught by checkCase and reported as the case's error.
func jsonValue(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("testkit: cannot compare a %T: %v", v, err))
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		panic(fmt.Sprintf("testkit: cannot compare a %T: %v", v, err))
	}
	return out
}

func equivalent(got, want any, policy string) bool {
	got, want = jsonValue(got), jsonValue(want)
	if policy == "unordered" || policy == "unordered-groups" {
		got = sortArray(got, policy == "unordered-groups")
		want = sortArray(want, policy == "unordered-groups")
	}
	return equalValue(got, want, policy == "float")
}

func sortArray(v any, inner bool) any {
	a, ok := v.([]any)
	if !ok {
		return v
	}
	if inner {
		for i := range a {
			a[i] = sortArray(a[i], false)
		}
	}
	sort.Slice(a, func(i, j int) bool {
		b, _ := json.Marshal(a[i])
		c, _ := json.Marshal(a[j])
		return string(b) < string(c)
	})
	return a
}

func equalValue(a, b any, tolerant bool) bool {
	if x, ok := a.([]any); ok {
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !equalValue(x[i], y[i], tolerant) {
				return false
			}
		}
		return true
	}
	x, ok := a.(float64)
	if ok {
		y, ok := b.(float64)
		if !ok {
			return false
		}
		if !tolerant || y == 0 {
			return x == y
		}
		return math.Abs(x-y) <= math.Abs(y)*1e-6
	}
	return reflect.DeepEqual(a, b)
}

func resultError(got, want any) error {
	b, _ := json.Marshal(got)
	c, _ := json.Marshal(want)
	return fmt.Errorf("got %s; want %s", b, c)
}

func compare(s testbank.Suite, c testbank.Case, args []reflect.Value, result reflect.Value) error {
	if s.Policy == "topological" || s.Policy == "alphabet" {
		// A solution may reorder its slices; validate against the original input.
		original := make([]reflect.Value, len(args))
		for i := range args {
			value, err := decode(c.Args[i], args[i].Type())
			if err != nil {
				return err
			}
			original[i] = value
		}
		args = original
	}
	var want any
	if err := json.Unmarshal(c.Want, &want); err != nil {
		return err
	}
	if s.Policy == "ancestor" {
		if result.IsNil() {
			return fmt.Errorf("got nil ancestor; want node %v", want)
		}
		found := findNode(args[0], int(want.(float64)))
		if !found.IsValid() || result.Pointer() != found.Pointer() {
			return fmt.Errorf("want the existing ancestor node %v", want)
		}
		return nil
	}
	got, err := normalize(result)
	if err != nil {
		return err
	}
	switch s.Policy {
	case "topological":
		return checkTopological(args, got, want)
	case "alphabet":
		return checkAlphabet(args, got, want)
	case "palindrome":
		text := args[0].String()
		answer, ok := got.(string)
		if !ok || len(answer) != len(want.(string)) || !strings.Contains(text, answer) {
			return resultError(got, want)
		}
		for i, j := 0, len(answer)-1; i < j; i, j = i+1, j-1 {
			if answer[i] != answer[j] {
				return fmt.Errorf("%q is not a palindrome", answer)
			}
		}
		return nil
	}
	policy := s.Policy
	if s.Policy == "graph-clone" {
		if !result.IsNil() && !args[0].IsNil() && result.Elem().FieldByName("Val").Int() != args[0].Elem().FieldByName("Val").Int() {
			return fmt.Errorf("cloned graph must return the copy of the original starting node")
		}
		// Adjacency order is not part of the graph, but every edge is.
		got = sortNeighbors(jsonValue(got))
		want = sortNeighbors(want)
	}
	if !equivalent(got, want, policy) {
		return resultError(got, want)
	}
	return nil
}

func sortNeighbors(v any) any {
	a, ok := v.([]any)
	if !ok {
		return v
	}
	for i := range a {
		a[i] = sortArray(a[i], false)
	}
	return a
}

func findNode(root reflect.Value, value int) reflect.Value {
	for _, n := range nodeSet(root) {
		if int(n.Elem().FieldByName("Val").Int()) == value {
			return n
		}
	}
	return reflect.Value{}
}

func checkTopological(args []reflect.Value, got, want any) error {
	a, ok := got.([]any)
	if !ok {
		return fmt.Errorf("order must be an array")
	}
	expected := want.([]any)
	if len(expected) == 0 {
		if len(a) == 0 {
			return nil
		}
		return fmt.Errorf("cyclic prerequisites require an empty order")
	}
	n := int(args[0].Int())
	pos := map[int]int{}
	if len(a) != n {
		return fmt.Errorf("order has %d courses; want %d", len(a), n)
	}
	for i, x := range a {
		v := int(reflect.ValueOf(x).Convert(reflect.TypeFor[int]()).Int())
		if v < 0 || v >= n {
			return fmt.Errorf("unknown course %d", v)
		}
		if _, ok := pos[v]; ok {
			return fmt.Errorf("course %d appears twice", v)
		}
		pos[v] = i
	}
	for i := 0; i < args[1].Len(); i++ {
		pair := args[1].Index(i)
		after, before := int(pair.Index(0).Int()), int(pair.Index(1).Int())
		if pos[before] >= pos[after] {
			return fmt.Errorf("course %d must precede %d", before, after)
		}
	}
	return nil
}

func checkAlphabet(args []reflect.Value, got, want any) error {
	order, ok := got.(string)
	if !ok {
		return fmt.Errorf("alphabet must be a string")
	}
	if want == "" {
		if order == "" {
			return nil
		}
		return fmt.Errorf("invalid dictionary requires empty alphabet")
	}
	words := args[0]
	chars := map[byte]bool{}
	for i := 0; i < words.Len(); i++ {
		for _, b := range []byte(words.Index(i).String()) {
			chars[b] = true
		}
	}
	rank := map[byte]int{}
	if len(order) != len(chars) {
		return fmt.Errorf("alphabet must include each of the %d letters once", len(chars))
	}
	for i, b := range []byte(order) {
		if !chars[b] {
			return fmt.Errorf("alphabet includes unknown letter %q", b)
		}
		if _, ok := rank[b]; ok {
			return fmt.Errorf("duplicate letter %q", b)
		}
		rank[b] = i
	}
	for i := 1; i < words.Len(); i++ {
		a, b := words.Index(i-1).String(), words.Index(i).String()
		j := 0
		for j < len(a) && j < len(b) && a[j] == b[j] {
			j++
		}
		if j == len(b) && j < len(a) {
			return fmt.Errorf("invalid prefix ordering")
		}
		if j < len(a) && j < len(b) && rank[a[j]] >= rank[b[j]] {
			return fmt.Errorf("alphabet does not order %q before %q", a, b)
		}
	}
	return nil
}
