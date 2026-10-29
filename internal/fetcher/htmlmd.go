// Package fetcher builds the local content tree: it reads the NeetCode 150
// list from GitHub, pulls each problem statement from LeetCode, and converts
// those statements to Markdown.
package fetcher

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// blockTags are the elements that start a new Markdown block. Anything else is
// treated as inline content. LeetCode statements use a small, stable tag set.
var blockTags = map[string]bool{
	"p": true, "div": true, "pre": true, "ul": true, "ol": true, "li": true,
	"table": true, "blockquote": true, "hr": true, "section": true, "article": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
}

// HTMLToMarkdown converts a LeetCode problem statement to Markdown.
func HTMLToMarkdown(in string) (string, error) {
	doc, err := html.Parse(strings.NewReader(in))
	if err != nil {
		return "", fmt.Errorf("parse statement html: %w", err)
	}
	body := findNode(doc, "body")
	if body == nil {
		body = doc
	}
	c := &conv{}
	c.blocks(body)
	return strings.Join(c.out, "\n\n") + "\n", nil
}

func findNode(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if got := findNode(ch, tag); got != nil {
			return got
		}
	}
	return nil
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// conv accumulates finished Markdown blocks.
type conv struct {
	out       []string
	listDepth int
}

func (c *conv) push(s string) {
	s = strings.Trim(s, " \t\n")
	if s != "" {
		c.out = append(c.out, s)
	}
}

// blocks walks n's children, gathering inline runs into paragraphs and
// dispatching block elements as they appear.
func (c *conv) blocks(n *html.Node) {
	var para strings.Builder
	flush := func() {
		c.push(tidyInline(para.String()))
		para.Reset()
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == html.ElementNode && blockTags[ch.Data] {
			flush()
			c.block(ch)
			continue
		}
		para.WriteString(c.inline(ch))
	}
	flush()
}

func (c *conv) block(n *html.Node) {
	switch n.Data {
	case "pre":
		c.push(fence(rawText(n)))
	case "ul":
		c.push(c.list(n, false))
	case "ol":
		c.push(c.list(n, true))
	case "table":
		c.push(c.table(n))
	case "hr":
		c.push("---")
	case "blockquote":
		sub := &conv{listDepth: c.listDepth}
		sub.blocks(n)
		var lines []string
		for _, b := range sub.out {
			for _, ln := range strings.Split(b, "\n") {
				lines = append(lines, strings.TrimRight("> "+ln, " "))
			}
			lines = append(lines, ">")
		}
		c.push(strings.Join(lines, "\n"))
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(n.Data[1] - '0')
		c.push(strings.Repeat("#", level) + " " + tidyInline(c.inlineChildren(n)))
	default: // p, div, li, section, article
		c.blocks(n)
	}
}

// list renders a <ul>/<ol>. Nested lists come back indented under their item.
func (c *conv) list(n *html.Node, ordered bool) string {
	var lines []string
	i := 1
	for li := n.FirstChild; li != nil; li = li.NextSibling {
		if li.Type != html.ElementNode || li.Data != "li" {
			continue
		}
		marker := "- "
		if ordered {
			marker = fmt.Sprintf("%d. ", i)
		}
		i++
		sub := &conv{listDepth: c.listDepth + 1}
		sub.blocks(li)
		item := strings.Join(sub.out, "\n")
		if item == "" {
			continue
		}
		indent := strings.Repeat(" ", len(marker))
		for j, ln := range strings.Split(item, "\n") {
			if j == 0 {
				lines = append(lines, marker+ln)
			} else {
				lines = append(lines, indent+ln)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// table renders a table as GFM. LeetCode uses these rarely and never nests them.
func (c *conv) table(n *html.Node) string {
	var rows [][]string
	var header []string
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			if ch.Type == html.ElementNode && ch.Data == "tr" {
				var cells []string
				isHeader := false
				for td := ch.FirstChild; td != nil; td = td.NextSibling {
					if td.Type != html.ElementNode || (td.Data != "td" && td.Data != "th") {
						continue
					}
					if td.Data == "th" {
						isHeader = true
					}
					cell := tidyInline(c.inlineChildren(td))
					cells = append(cells, strings.ReplaceAll(cell, "|", "\\|"))
				}
				if len(cells) == 0 {
					continue
				}
				if isHeader && header == nil {
					header = cells
				} else {
					rows = append(rows, cells)
				}
				continue
			}
			walk(ch)
		}
	}
	walk(n)
	if header == nil {
		if len(rows) == 0 {
			return ""
		}
		header, rows = rows[0], rows[1:]
	}
	var b strings.Builder
	b.WriteString("| " + strings.Join(header, " | ") + " |\n|")
	for range header {
		b.WriteString(" --- |")
	}
	for _, r := range rows {
		for len(r) < len(header) {
			r = append(r, "")
		}
		b.WriteString("\n| " + strings.Join(r[:len(header)], " | ") + " |")
	}
	return b.String()
}

// inline renders a node that carries no block structure.
func (c *conv) inline(n *html.Node) string {
	switch n.Type {
	case html.TextNode:
		return escapeText(n.Data)
	case html.CommentNode, html.DoctypeNode:
		return ""
	case html.ElementNode:
		switch n.Data {
		case "code":
			return codeSpan(rawText(n))
		case "strong", "b":
			return wrap(c.inlineChildren(n), "**")
		case "em", "i", "var":
			return wrap(c.inlineChildren(n), "*")
		case "sup":
			return supScript(rawText(n))
		case "sub":
			if t := strings.TrimSpace(rawText(n)); t != "" {
				return "_" + t
			}
			return ""
		case "br":
			return "\n"
		case "img":
			return fmt.Sprintf("![%s](%s)", strings.TrimSpace(attr(n, "alt")), attr(n, "src"))
		case "a":
			text := strings.TrimSpace(c.inlineChildren(n))
			href := attr(n, "href")
			if href == "" || text == "" {
				return text
			}
			return fmt.Sprintf("[%s](%s)", text, href)
		case "style", "script":
			return ""
		}
		return c.inlineChildren(n)
	}
	return c.inlineChildren(n)
}

func (c *conv) inlineChildren(n *html.Node) string {
	var b strings.Builder
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		b.WriteString(c.inline(ch))
	}
	return b.String()
}

// wrap applies an emphasis marker, keeping the surrounding spaces outside it so
// the delimiters stay adjacent to the word (`** x **` would not render).
func wrap(s, marker string) string {
	trimmed := strings.Trim(s, " \t\n")
	if trimmed == "" {
		return s
	}
	lead, tail := leadingSpace(s), trailingSpace(s)
	return lead + marker + trimmed + marker + tail
}

func leadingSpace(s string) string {
	if t := strings.TrimLeft(s, " \t\n"); len(t) < len(s) {
		return " "
	}
	return ""
}

func trailingSpace(s string) string {
	if t := strings.TrimRight(s, " \t\n"); len(t) < len(s) {
		return " "
	}
	return ""
}

// codeSpan wraps text in the shortest backtick run that can hold it.
func codeSpan(s string) string {
	s = strings.TrimSpace(replaceNBSP(s))
	if s == "" {
		return ""
	}
	ticks := "`"
	for strings.Contains(s, ticks) {
		ticks += "`"
	}
	pad := ""
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		pad = " "
	}
	return ticks + pad + s + pad + ticks
}

// supScript renders exponents as ^n, so `10<sup>4</sup>` reads as 10^4.
func supScript(s string) string {
	s = strings.TrimSpace(replaceNBSP(s))
	if s == "" {
		return ""
	}
	return "^" + s
}

var fenceRE = regexp.MustCompile("(?m)^`{3,}")

// fence wraps preformatted text in a code fence long enough to contain it.
func fence(s string) string {
	s = strings.Trim(replaceNBSP(s), "\n")
	if s == "" {
		return ""
	}
	marks := "```"
	for _, m := range fenceRE.FindAllString(s, -1) {
		for len(marks) <= len(m) {
			marks += "`"
		}
	}
	return marks + "\n" + s + "\n" + marks
}

// rawText is the concatenated text of a subtree, whitespace preserved.
func rawText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			switch ch.Type {
			case html.TextNode:
				b.WriteString(ch.Data)
			case html.ElementNode:
				switch ch.Data {
				case "br":
					b.WriteString("\n")
				case "sup":
					// Keep exponents readable: 10<sup>4</sup> must not flatten to 104.
					b.WriteString("^" + strings.TrimSpace(rawText(ch)))
				case "sub":
					b.WriteString("_" + strings.TrimSpace(rawText(ch)))
				default:
					walk(ch)
				}
			}
		}
	}
	walk(n)
	return b.String()
}

func replaceNBSP(s string) string { return strings.ReplaceAll(s, " ", " ") }

// mdEscape are the characters that would otherwise be read as Markdown syntax.
// Angle brackets are deliberately left alone: constraints are full of `<=`,
// which no Markdown parser mistakes for a tag, and escaping them would make the
// files unpleasant to read as plain text.
var mdEscape = strings.NewReplacer(
	`\`, `\\`,
	"`", "\\`",
	`*`, `\*`,
	`_`, `\_`,
	`[`, `\[`,
	`]`, `\]`,
)

func escapeText(s string) string { return mdEscape.Replace(replaceNBSP(s)) }

var multiSpace = regexp.MustCompile(`[ \t]+`)

// tidyInline collapses the incidental whitespace that HTML source carries,
// without touching the hard line breaks that <br> produced.
func tidyInline(s string) string {
	lines := strings.Split(s, "\n")
	kept := make([]string, 0, len(lines))
	for _, ln := range lines {
		kept = append(kept, strings.TrimSpace(multiSpace.ReplaceAllString(ln, " ")))
	}
	// Drop blank lines at both ends but keep single breaks inside.
	for len(kept) > 0 && kept[0] == "" {
		kept = kept[1:]
	}
	for len(kept) > 0 && kept[len(kept)-1] == "" {
		kept = kept[:len(kept)-1]
	}
	out := strings.Join(kept, "\n")
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return out
}
