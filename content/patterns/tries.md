---
title: Tries
---

# Tries

A trie stores a set of strings as a tree of characters, so that a shared prefix is stored once. Looking up a word costs `O(len(word))` regardless of how many words are in the structure — the size of the dictionary does not appear in the complexity at all.

That is the whole reason to build one. A hash set answers "is this exact word present?" just as fast. A trie also answers "is this a *prefix* of anything present?", which is what prefix search, autocomplete and grid-word search actually need.

## When to reach for it

- "Implement prefix search / autocomplete / starts-with."
- Many queries against one fixed dictionary — build once, query repeatedly.
- Wildcard matching within words, where a `.` branches to every child.
- Searching a grid for many words at once: walk the grid and the trie in step, and prune the moment the current path is not a prefix of anything.
- Bitwise problems framed as a binary trie over the bits of each number.

## The shape

```go
type Trie struct {
    children [26]*Trie // or map[byte]*Trie for a wider alphabet
    isWord   bool      // true if a word ends exactly here
}

func (t *Trie) Insert(word string) {
    node := t
    for i := 0; i < len(word); i++ {
        c := word[i] - 'a'
        if node.children[c] == nil {
            node.children[c] = &Trie{}
        }
        node = node.children[c]
    }
    node.isWord = true
}

// find walks the trie and returns the node the prefix ends at, or nil.
func (t *Trie) find(prefix string) *Trie {
    node := t
    for i := 0; i < len(prefix); i++ {
        node = node.children[prefix[i]-'a']
        if node == nil {
            return nil
        }
    }
    return node
}

func (t *Trie) Search(word string) bool   { n := t.find(word); return n != nil && n.isWord }
func (t *Trie) StartsWith(p string) bool  { return t.find(p) != nil }
```

```go
// Wildcard search: a '.' means try every child.
func (t *Trie) match(word string) bool {
    node := t
    for i := 0; i < len(word); i++ {
        if word[i] == '.' {
            for _, child := range node.children {
                if child != nil && child.match(word[i+1:]) {
                    return true
                }
            }
            return false
        }
        node = node.children[word[i]-'a']
        if node == nil {
            return false
        }
    }
    return node.isWord
}
```

## Complexity

Insert and search are `O(m)` for a word of length `m`. Building the trie is `O(total characters)`. Space is `O(total characters × alphabet)` in the array form — generous, but it buys array-speed child lookup with no hashing.

The payoff is in combined search: checking 10,000 words against a grid with a trie prunes dead paths immediately, where testing each word separately re-walks the same prefixes thousands of times.

## Go mechanics

- `[26]*Trie` is a value array inside the struct — no allocation per node beyond the node itself, and `nil` means absent. Use `map[byte]*Trie` only if the alphabet is large or sparse.
- `word[i] - 'a'` yields a `byte`; indexing an array with it is fine. Validate the input range if the problem does not guarantee lowercase.
- Keep `isWord` separate from "has no children". A word can be a strict prefix of another word (`"car"` inside `"card"`), so the absence of children says nothing.
- Methods on `*Trie` with the root as an ordinary node means no separate root type is needed.
- `for _, child := range node.children` iterates all 26 slots including nils — cheap, and deterministic in order, unlike a map.

## Where it goes wrong

- Using "no children" as the end-of-word test, which breaks on prefix words.
- Forgetting to mark `isWord` at the end of `Insert`, so nothing is ever found.
- Returning `true` from `Search` for a prefix that is not itself a word — that is what `StartsWith` is for.
- Building a fresh trie inside a query loop instead of once outside it.
- In grid search, failing to unmark a visited cell on the way back out, or failing to prune when the trie node goes nil.
