// Package testkit runs table-driven practice cases using only the standard library.
// Reflection lets the same fixtures work with the local LeetCode node types.
package testkit

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

func decode(raw json.RawMessage, typ reflect.Type) (reflect.Value, error) {
	if typ.Kind() == reflect.Pointer && typ.Elem().Kind() == reflect.Struct {
		var data []json.RawMessage
		if err := json.Unmarshal(raw, &data); err != nil {
			return reflect.Value{}, err
		}
		if len(data) == 0 {
			return reflect.Zero(typ), nil
		}
		if _, ok := typ.Elem().FieldByName("Left"); ok {
			return decodeTree(data, typ)
		}
		if _, ok := typ.Elem().FieldByName("Neighbors"); ok {
			return decodeGraph(data, typ)
		}
		if _, ok := typ.Elem().FieldByName("Random"); ok {
			return decodeRandom(data, typ)
		}
		if _, ok := typ.Elem().FieldByName("Next"); ok {
			nodes := make([]reflect.Value, len(data))
			for i, v := range data {
				n, err := valueNode(v, typ)
				if err != nil {
					return reflect.Value{}, err
				}
				nodes[i] = n
			}
			for i := 1; i < len(nodes); i++ {
				nodes[i-1].Elem().FieldByName("Next").Set(nodes[i])
			}
			return nodes[0], nil
		}
	}
	if typ.Kind() == reflect.Slice {
		var data []json.RawMessage
		if err := json.Unmarshal(raw, &data); err != nil {
			return reflect.Value{}, err
		}
		out := reflect.MakeSlice(typ, len(data), len(data))
		for i, v := range data {
			elem, err := decode(v, typ.Elem())
			if err != nil {
				return reflect.Value{}, err
			}
			out.Index(i).Set(elem)
		}
		return out, nil
	}
	if typ.Kind() == reflect.Uint8 && len(raw) > 0 && raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return reflect.Value{}, err
		}
		if len(s) != 1 {
			return reflect.Value{}, fmt.Errorf("byte fixture must contain one ASCII byte: %q", s)
		}
		out := reflect.New(typ).Elem()
		out.SetUint(uint64(s[0]))
		return out, nil
	}
	out := reflect.New(typ)
	if err := json.Unmarshal(raw, out.Interface()); err != nil {
		return reflect.Value{}, err
	}
	return out.Elem(), nil
}

func valueNode(raw json.RawMessage, typ reflect.Type) (reflect.Value, error) {
	out := reflect.New(typ.Elem())
	v, err := decode(raw, out.Elem().FieldByName("Val").Type())
	if err != nil {
		return reflect.Value{}, err
	}
	out.Elem().FieldByName("Val").Set(v)
	return out, nil
}

func decodeTree(data []json.RawMessage, typ reflect.Type) (reflect.Value, error) {
	if string(data[0]) == "null" {
		return reflect.Zero(typ), nil
	}
	root, err := valueNode(data[0], typ)
	if err != nil {
		return reflect.Value{}, err
	}
	queue := []reflect.Value{root}
	pos := 1
	for len(queue) > 0 && pos < len(data) {
		n := queue[0]
		queue = queue[1:]
		for _, field := range []string{"Left", "Right"} {
			if pos >= len(data) {
				break
			}
			raw := data[pos]
			pos++
			if string(raw) == "null" {
				continue
			}
			child, err := valueNode(raw, typ)
			if err != nil {
				return reflect.Value{}, err
			}
			n.Elem().FieldByName(field).Set(child)
			queue = append(queue, child)
		}
	}
	if pos < len(data) {
		return reflect.Value{}, fmt.Errorf("unreachable tree fixture values")
	}
	return root, nil
}

func decodeGraph(data []json.RawMessage, typ reflect.Type) (reflect.Value, error) {
	nodes := make([]reflect.Value, len(data))
	for i := range nodes {
		nodes[i], _ = valueNode(json.RawMessage(fmt.Sprint(i+1)), typ)
	}
	for i, raw := range data {
		var edges []int
		if err := json.Unmarshal(raw, &edges); err != nil {
			return reflect.Value{}, err
		}
		neighbors := nodes[i].Elem().FieldByName("Neighbors")
		for _, v := range edges {
			if v < 1 || v > len(nodes) {
				return reflect.Value{}, fmt.Errorf("invalid graph neighbor %d", v)
			}
			neighbors.Set(reflect.Append(neighbors, nodes[v-1]))
		}
	}
	return nodes[0], nil
}

func decodeRandom(data []json.RawMessage, typ reflect.Type) (reflect.Value, error) {
	nodes := make([]reflect.Value, len(data))
	pairs := make([][]json.RawMessage, len(data))
	for i, raw := range data {
		if err := json.Unmarshal(raw, &pairs[i]); err != nil {
			return reflect.Value{}, err
		}
		if len(pairs[i]) != 2 {
			return reflect.Value{}, fmt.Errorf("random node needs value and pointer")
		}
		n, err := valueNode(pairs[i][0], typ)
		if err != nil {
			return reflect.Value{}, err
		}
		nodes[i] = n
	}
	for i, n := range nodes {
		if i+1 < len(nodes) {
			n.Elem().FieldByName("Next").Set(nodes[i+1])
		}
		if string(pairs[i][1]) == "null" {
			continue
		}
		var idx int
		if err := json.Unmarshal(pairs[i][1], &idx); err != nil {
			return reflect.Value{}, err
		}
		if idx < 0 || idx >= len(nodes) {
			return reflect.Value{}, fmt.Errorf("invalid random pointer %d", idx)
		}
		n.Elem().FieldByName("Random").Set(nodes[idx])
	}
	return nodes[0], nil
}

func nodeSet(root reflect.Value) map[uintptr]reflect.Value {
	seen := map[uintptr]reflect.Value{}
	queue := []reflect.Value{root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if !n.IsValid() || n.Kind() != reflect.Pointer || n.IsNil() {
			continue
		}
		if _, ok := seen[n.Pointer()]; ok {
			continue
		}
		seen[n.Pointer()] = n
		for _, f := range []string{"Next", "Random", "Left", "Right"} {
			v := n.Elem().FieldByName(f)
			if v.IsValid() {
				queue = append(queue, v)
			}
		}
		v := n.Elem().FieldByName("Neighbors")
		if v.IsValid() {
			for i := 0; i < v.Len(); i++ {
				queue = append(queue, v.Index(i))
			}
		}
		// Output traversals are bounded even when an incorrect solution grows cycles.
		if len(seen) > 100000 {
			break
		}
	}
	return seen
}

func normalize(v reflect.Value) (any, error) {
	if !v.IsValid() {
		return nil, nil
	}
	if v.Kind() == reflect.Interface {
		return normalize(v.Elem())
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return []any{}, nil
		}
		n := v.Elem()
		if n.FieldByName("Left").IsValid() {
			return normalizeTree(v)
		}
		if n.FieldByName("Neighbors").IsValid() {
			return normalizeGraph(v)
		}
		if n.FieldByName("Random").IsValid() {
			return normalizeRandom(v)
		}
		if n.FieldByName("Next").IsValid() {
			nodes, err := listNodes(v)
			if err != nil {
				return nil, err
			}
			out := make([]any, len(nodes))
			for i, n := range nodes {
				out[i] = n.Elem().FieldByName("Val").Interface()
			}
			return out, nil
		}
		return nil, fmt.Errorf("unsupported node type %v", v.Type())
	}
	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		out := make([]any, v.Len())
		for i := range out {
			elem, err := normalize(v.Index(i))
			if err != nil {
				return nil, err
			}
			out[i] = elem
		}
		return out, nil
	}
	// Byte boards are represented by letters in the fixtures, never base64.
	if v.Kind() == reflect.Uint8 {
		return string(byte(v.Uint())), nil
	}
	return v.Interface(), nil
}

func listNodes(root reflect.Value) ([]reflect.Value, error) {
	var out []reflect.Value
	seen := map[uintptr]bool{}
	for n := root; !n.IsNil(); n = n.Elem().FieldByName("Next") {
		if seen[n.Pointer()] {
			return nil, fmt.Errorf("returned list contains a cycle")
		}
		if len(out) >= 100000 {
			return nil, fmt.Errorf("returned list exceeds fixture limit")
		}
		seen[n.Pointer()] = true
		out = append(out, n)
	}
	return out, nil
}

func normalizeTree(root reflect.Value) (any, error) {
	out := []any{}
	queue := []reflect.Value{root}
	seen := map[uintptr]bool{}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if n.IsNil() {
			out = append(out, nil)
			continue
		}
		if seen[n.Pointer()] || len(seen) >= 100000 {
			return nil, fmt.Errorf("returned tree contains a cycle or shared node")
		}
		seen[n.Pointer()] = true
		out = append(out, n.Elem().FieldByName("Val").Interface())
		queue = append(queue, n.Elem().FieldByName("Left"), n.Elem().FieldByName("Right"))
	}
	for len(out) > 0 && out[len(out)-1] == nil {
		out = out[:len(out)-1]
	}
	return out, nil
}

func normalizeGraph(root reflect.Value) (any, error) {
	nodes := nodeSet(root)
	out := make([]any, len(nodes))
	used := map[int]bool{}
	for _, n := range nodes {
		val := int(n.Elem().FieldByName("Val").Int())
		if val < 1 || val > len(nodes) || used[val] {
			return nil, fmt.Errorf("invalid or duplicate cloned node value %d", val)
		}
		used[val] = true
		adj := n.Elem().FieldByName("Neighbors")
		edges := make([]int, adj.Len())
		for i := range edges {
			if adj.Index(i).IsNil() {
				return nil, fmt.Errorf("nil graph neighbor")
			}
			edges[i] = int(adj.Index(i).Elem().FieldByName("Val").Int())
		}
		sort.Ints(edges)
		out[val-1] = edges
	}
	return out, nil
}

func normalizeRandom(root reflect.Value) (any, error) {
	nodes, err := listNodes(root)
	if err != nil {
		return nil, err
	}
	indices := map[uintptr]int{}
	for i, n := range nodes {
		indices[n.Pointer()] = i
	}
	out := make([]any, len(nodes))
	for i, n := range nodes {
		var idx any
		random := n.Elem().FieldByName("Random")
		if !random.IsNil() {
			k, ok := indices[random.Pointer()]
			if !ok {
				return nil, fmt.Errorf("random pointer points outside cloned list")
			}
			idx = k
		}
		out[i] = []any{n.Elem().FieldByName("Val").Interface(), idx}
	}
	return out, nil
}
