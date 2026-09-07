package musicbrainz

import (
	"bytes"
	"slices"
	"strings"

	"golang.org/x/net/html"

	"github.com/platten/playlistai/internal/core"
)

// MusicBrainz's public genre pages expose relationships that its documented
// JSON genre API does not. Only labeled relationship rows are interpreted.
func readGenrePage(raw []byte, id, base string, graph *core.GenreGraph, aliases bool) {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return
	}
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tr" {
			if aliases {
				if n.Parent != nil && n.Parent.Data == "tbody" && n.Parent.Parent != nil && nodeAttr(n.Parent.Parent, "class") == "tbl" {
					for cell := n.FirstChild; cell != nil; cell = cell.NextSibling {
						if cell.Data == "td" {
							for i := range graph.Nodes {
								if graph.Nodes[i].ID == id {
									if alias := nodeText(cell); !slices.Contains(graph.Nodes[i].Aliases, alias) {
										graph.Nodes[i].Aliases = append(graph.Nodes[i].Aliases, alias)
									}
								}
							}
							break
						}
					}
				}
			} else {
				label := ""
				for cell := n.FirstChild; cell != nil; cell = cell.NextSibling {
					if cell.Data == "th" {
						label = strings.TrimSuffix(strings.ToLower(nodeText(cell)), ":")
					}
				}
				kind, reverse := "", false
				switch label {
				case "subgenre of":
					kind = "subgenre"
				case "subgenres":
					kind, reverse = "subgenre", true
				case "influenced by":
					kind = "influence"
				case "influenced genres":
					kind, reverse = "influence", true
				case "fusion of":
					kind = "fusion"
				case "has fusion genres":
					kind, reverse = "fusion", true
				}
				if kind != "" {
					var links func(*html.Node)
					links = func(link *html.Node) {
						if link.Data == "a" {
							other, ok := strings.CutPrefix(nodeAttr(link, "href"), "/genre/")
							if ok && other != "" && !strings.Contains(other, "/") {
								from, to := id, other
								if reverse {
									from, to = to, from
								}
								graph.Relations = append(graph.Relations, core.GenreRelation{From: from, To: to, Kind: kind, Source: base + "/genre/" + id})
							}
						}
						for child := link.FirstChild; child != nil; child = child.NextSibling {
							links(child)
						}
					}
					links(n)
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
}

func nodeAttr(n *html.Node, name string) string {
	for _, attr := range n.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}
func nodeText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var text strings.Builder
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		text.WriteString(nodeText(child))
	}
	return strings.TrimSpace(text.String())
}
