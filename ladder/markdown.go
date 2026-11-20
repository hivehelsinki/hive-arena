package main

import (
	"html"
	"html/template"
	"regexp"
	"strings"
)

var (
	codeSpan = regexp.MustCompile("`([^`]+)`")
	boldSpan = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	italSpan = regexp.MustCompile(`\*([^*]+)\*`)
	linkSpan = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

func inlineMarkdown(s string) string {
	s = html.EscapeString(s)
	s = codeSpan.ReplaceAllString(s, "<code>$1</code>")
	s = boldSpan.ReplaceAllString(s, "<strong>$1</strong>")
	s = italSpan.ReplaceAllString(s, "<em>$1</em>")
	// Links to other documents are not served by the ladder: keep their text
	return linkSpan.ReplaceAllString(s, "$1")
}

func tableCells(line string) []string {
	cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// renderMarkdown converts the small subset of Markdown used by the rules:
// headings (shifted down by shift levels), paragraphs, bullet lists
// (with continuation lines), tables and inline code, bold and italic
func renderMarkdown(source string, shift int) template.HTML {
	var out strings.Builder
	lines := strings.Split(source, "\n")
	inList := false
	closeList := func() {
		if inList {
			out.WriteString("</li></ul>\n")
			inList = false
		}
	}
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			closeList()
		case strings.HasPrefix(trimmed, "#"):
			closeList()
			level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
			level = min(level+shift, 6)
			out.WriteString("<h" + string(rune('0'+level)) + ">" + inlineMarkdown(strings.TrimSpace(trimmed[len(trimmed)-len(strings.TrimLeft(trimmed, "# ")):])) + "</h" + string(rune('0'+level)) + ">\n")
		case strings.HasPrefix(trimmed, "- "):
			if inList {
				out.WriteString("</li>\n")
			} else {
				out.WriteString("<ul>\n")
				inList = true
			}
			out.WriteString("<li>" + inlineMarkdown(trimmed[2:]))
		case strings.HasPrefix(trimmed, "|"):
			closeList()
			out.WriteString("<table>\n")
			for first := true; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
				cells := tableCells(lines[i])
				if strings.Trim(cells[0], "-: ") == "" {
					continue
				}
				tag := "td"
				if first {
					tag = "th"
					first = false
				}
				out.WriteString("<tr>")
				for _, c := range cells {
					out.WriteString("<" + tag + ">" + inlineMarkdown(c) + "</" + tag + ">")
				}
				out.WriteString("</tr>\n")
			}
			i--
			out.WriteString("</table>\n")
		case inList:
			out.WriteString(" " + inlineMarkdown(trimmed))
		default:
			// A paragraph, up to the next blank line
			para := []string{trimmed}
			for i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" && !strings.HasPrefix(strings.TrimSpace(lines[i+1]), "#") &&
				!strings.HasPrefix(strings.TrimSpace(lines[i+1]), "- ") && !strings.HasPrefix(strings.TrimSpace(lines[i+1]), "|") {
				i++
				para = append(para, strings.TrimSpace(lines[i]))
			}
			out.WriteString("<p>" + inlineMarkdown(strings.Join(para, " ")) + "</p>\n")
		}
	}
	closeList()
	return template.HTML(out.String())
}
