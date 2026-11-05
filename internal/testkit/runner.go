package testkit

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"neetcode150/internal/testbank"
)

// Run executes each fixture independently, reporting panics as case failures.
// Codec cases assert round trips rather than prescribing an encoding format.
func Run(t *testing.T, spec string, functions ...any) {
	t.Helper()
	var s testbank.Suite
	if err := json.Unmarshal([]byte(spec), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Cases) == 0 {
		t.Fatal("practice suite has no cases")
	}
	for _, c := range s.Cases {
		t.Run(c.Kind+"/"+c.Name, func(t *testing.T) {
			if err := checkCase(s, c, false, functions...); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Validate checks every fixture against the actual Go signature without calling
// the solution. This also checks method arguments for stateful problems.
func Validate(spec string, functions ...any) error {
	var s testbank.Suite
	if err := json.Unmarshal([]byte(spec), &s); err != nil {
		return err
	}
	for _, c := range s.Cases {
		if err := checkCase(s, c, true, functions...); err != nil {
			return fmt.Errorf("%s/%s: %w", s.Slug, c.Name, err)
		}
	}
	return nil
}

func checkCase(s testbank.Suite, c testbank.Case, validate bool, functions ...any) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	if len(functions) == 0 {
		return fmt.Errorf("no solution entry point")
	}
	fn := reflect.ValueOf(functions[0])
	if s.Mode == "operations" {
		return checkOperations(c, fn, validate)
	}
	rawArgs := append([]json.RawMessage(nil), c.Args...)
	if s.Policy == "ancestor" && len(rawArgs) == 3 {
		rawArgs[1], rawArgs[2] = json.RawMessage("[]"), json.RawMessage("[]")
	}
	args, err := arguments(rawArgs, fn.Type())
	if err != nil {
		return err
	}
	var expectedType reflect.Type
	if s.Policy == "ancestor" {
		expectedType = reflect.TypeFor[int]()
	} else if s.Mutates || s.Mode == "strings-codec" || s.Mode == "tree-codec" {
		if len(args) == 0 {
			return fmt.Errorf("missing input argument")
		}
		expectedType = args[0].Type()
	} else {
		if fn.Type().NumOut() != 1 {
			return fmt.Errorf("entry point must return one value")
		}
		expectedType = fn.Type().Out(0)
	}
	if _, err := decode(c.Want, expectedType); err != nil {
		return fmt.Errorf("expected output (%v): %w", expectedType, err)
	}
	if s.Policy == "ancestor" {
		for i := 1; i < 3; i++ {
			var value int
			if err := json.Unmarshal(c.Args[i], &value); err != nil {
				return err
			}
			args[i] = findNode(args[0], value)
			if !args[i].IsValid() {
				return fmt.Errorf("ancestor fixture refers to missing node %d", value)
			}
		}
	}
	if s.Policy == "cycle" && c.CyclePos != nil && *c.CyclePos >= 0 {
		nodes, err := listNodes(args[0])
		if err != nil {
			return err
		}
		if *c.CyclePos >= len(nodes) {
			return fmt.Errorf("cycle index out of bounds")
		}
		nodes[len(nodes)-1].Elem().FieldByName("Next").Set(nodes[*c.CyclePos])
	}
	if s.Mode == "strings-codec" || s.Mode == "tree-codec" {
		if len(functions) != 2 {
			return fmt.Errorf("codec needs encoder and decoder")
		}
		decoder := reflect.ValueOf(functions[1])
		if fn.Type().NumOut() != 1 || decoder.Type().NumIn() != 1 || decoder.Type().NumOut() != 1 || fn.Type().Out(0) != decoder.Type().In(0) || decoder.Type().Out(0) != args[0].Type() {
			return fmt.Errorf("incompatible codec signatures")
		}
		if validate {
			return nil
		}
		before, err := normalize(args[0])
		if err != nil {
			return err
		}
		encoded := fn.Call(args)
		got := decoder.Call(encoded)[0]
		normalized, err := normalize(got)
		if err != nil {
			return err
		}
		if !equivalent(normalized, before, "") {
			return resultError(normalized, before)
		}
		return nil
	}
	if validate {
		return nil
	}
	var original map[uintptr]reflect.Value
	var before any
	if s.Policy == "random-clone" || s.Policy == "graph-clone" {
		original = nodeSet(args[0])
		before, err = normalize(args[0])
		if err != nil {
			return err
		}
	}
	var reorder []reflect.Value
	var reorderValues []int64
	if s.Policy == "reorder" {
		reorder, err = listNodes(args[0])
		if err != nil {
			return err
		}
		for _, n := range reorder {
			reorderValues = append(reorderValues, n.Elem().FieldByName("Val").Int())
		}
	}
	out := fn.Call(args)
	var result reflect.Value
	if s.Mutates {
		result = args[0]
	} else {
		if len(out) != 1 {
			return fmt.Errorf("entry point must return one value")
		}
		result = out[0]
	}
	if original != nil {
		for ptr := range nodeSet(result) {
			if _, ok := original[ptr]; ok {
				return fmt.Errorf("result reuses an original node; a deep copy is required")
			}
		}
		after, e := normalize(args[0])
		if e != nil {
			return fmt.Errorf("input corrupted: %w", e)
		}
		if !equivalent(after, before, "") {
			return fmt.Errorf("copy changed the original structure")
		}
	}
	if s.Policy == "reorder" {
		after, e := listNodes(args[0])
		if e != nil {
			return e
		}
		if len(after) != len(reorder) {
			return fmt.Errorf("reorder changed the number of nodes")
		}
		for i, n := range after {
			idx := i / 2
			if i%2 == 1 {
				idx = len(reorder) - 1 - i/2
			}
			if n.Pointer() != reorder[idx].Pointer() {
				return fmt.Errorf("node %d is not the original node from position %d", i, idx)
			}
			if n.Elem().FieldByName("Val").Int() != reorderValues[idx] {
				return fmt.Errorf("reorder must move nodes without changing their values")
			}
		}
	}
	return compare(s, c, args, result)
}

func arguments(raw []json.RawMessage, fn reflect.Type) ([]reflect.Value, error) {
	if fn.Kind() != reflect.Func {
		return nil, fmt.Errorf("entry point is not a function")
	}
	if len(raw) != fn.NumIn() {
		return nil, fmt.Errorf("fixture has %d arguments; signature requires %d", len(raw), fn.NumIn())
	}
	args := make([]reflect.Value, len(raw))
	for i, v := range raw {
		a, err := decode(v, fn.In(i))
		if err != nil {
			return nil, fmt.Errorf("argument %d (%v): %w", i+1, fn.In(i), err)
		}
		args[i] = a
	}
	return args, nil
}

func checkOperations(c testbank.Case, constructor reflect.Value, validate bool) error {
	if len(c.Ops) == 0 || len(c.Ops) != len(c.Calls) {
		return fmt.Errorf("operations and argument lists differ")
	}
	var wants []json.RawMessage
	if err := json.Unmarshal(c.Want, &wants); err != nil {
		return err
	}
	if len(wants) != len(c.Ops) {
		return fmt.Errorf("operation outputs differ in length")
	}
	if string(wants[0]) != "null" {
		return fmt.Errorf("constructor output must be null")
	}
	args, err := arguments(c.Calls[0], constructor.Type())
	if err != nil {
		return err
	}
	if constructor.Type().NumOut() != 1 {
		return fmt.Errorf("constructor must return one object")
	}
	typ := constructor.Type().Out(0)
	var object reflect.Value
	if !validate {
		object = constructor.Call(args)[0]
		if object.Kind() != reflect.Pointer {
			ptr := reflect.New(typ)
			ptr.Elem().Set(object)
			object = ptr
		}
	}
	if typ.Kind() != reflect.Pointer {
		typ = reflect.PointerTo(typ)
	}
	for i := 1; i < len(c.Ops); i++ {
		name := strings.ToUpper(c.Ops[i][:1]) + c.Ops[i][1:]
		method, ok := typ.MethodByName(name)
		if !ok {
			return fmt.Errorf("missing method %s", name)
		}
		// An unbound method's first parameter is its receiver.
		raw := append([]json.RawMessage{json.RawMessage("null")}, c.Calls[i]...)
		a, err := arguments(raw, method.Type)
		if err != nil {
			return fmt.Errorf("operation %d %s: %w", i, name, err)
		}
		if method.Type.NumOut() == 0 {
			if string(wants[i]) != "null" {
				return fmt.Errorf("void method %s output must be null", name)
			}
		} else if method.Type.NumOut() == 1 {
			if _, err := decode(wants[i], method.Type.Out(0)); err != nil {
				return fmt.Errorf("%s expected output: %w", name, err)
			}
		}
		if validate {
			continue
		}
		a[0] = object
		out := method.Func.Call(a)
		var got any
		policy := ""
		if len(out) == 1 {
			got, err = normalize(out[0])
			if err != nil {
				return err
			}
			if out[0].Kind() == reflect.Float64 || out[0].Kind() == reflect.Float32 {
				policy = "float"
			}
		} else if len(out) > 1 {
			return fmt.Errorf("method %s returns too many values", name)
		}
		var want any
		if err := json.Unmarshal(wants[i], &want); err != nil {
			return err
		}
		if !equivalent(got, want, policy) {
			return fmt.Errorf("operation %d %s: %w", i, name, resultError(got, want))
		}
	}
	return nil
}
