package app

import (
	"image/color"
	"path/filepath"
	"sort"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

type fileView int

const (
	fileViewFlat fileView = iota
	fileViewFolder
	fileViewGrouped
	fileViewCount
)

func (c Config) startFileView() fileView {
	switch c.FileView {
	case "flat":
		return fileViewFlat
	case "folder":
		return fileViewFolder
	case "grouped":
		return fileViewGrouped
	}
	if c.FolderView {
		return fileViewFolder
	}
	return fileViewFlat
}

// groupedDocItem retains the file's actions and color but moves its suffix into
// the parent heading. Its DocFile still carries the root-relative rename context.
type groupedDocItem struct{ docItem }

func (i groupedDocItem) SuffixText() string { return "" }

type docFolderItem struct {
	components.CompactItem
	path       string
	titleColor func(string) color.Color
}

func (i docFolderItem) TitleColor() color.Color { return i.titleColor(i.path) }
func (i docFolderItem) KeepColor() bool         { return true }

func (s *homeScreen) docFolderColor(path string) color.Color {
	return gitStateColor(s.gitDocs.snapshot.state(path, true))
}

// groupedDocNodes projects the existing scan into independent, one-level groups.
// Absolute IDs keep selection and folds stable when the scan is refreshed.
func (s *homeScreen) groupedDocNodes(c *Ctx) []components.TreeNode {
	groups := make(map[string]*components.TreeNode)
	for _, doc := range c.Files {
		dir := filepath.Dir(doc.Path)
		group := groups[dir]
		if group == nil {
			rel, err := filepath.Rel(doc.Root, dir)
			if err != nil {
				rel = dir
			}
			group = &components.TreeNode{
				ID: "dir:" + dir,
				Item: docFolderItem{CompactItem: components.CompactItem{Name: filepath.ToSlash(rel)},
					path: dir, titleColor: s.docFolderColor},
			}
			groups[dir] = group
		}
		group.Children = append(group.Children, components.TreeNode{
			ID:   "file:" + doc.Path,
			Item: groupedDocItem{docItem{doc: doc, titleColor: s.docTitleColor}},
		})
	}
	nodes := make([]components.TreeNode, 0, len(groups))
	for _, group := range groups {
		sort.Slice(group.Children, func(i, j int) bool {
			return group.Children[i].Item.Title() < group.Children[j].Item.Title()
		})
		nodes = append(nodes, *group)
	}
	sort.Slice(nodes, func(i, j int) bool {
		a, b := nodes[i].Item.Title(), nodes[j].Item.Title()
		if a == "." || b == "." {
			return a == "." && b != "."
		}
		return a < b
	})
	return nodes
}

func (s *homeScreen) newGroupedPanel(c *Ctx) *components.TreePanel {
	return components.NewTreePanel(s.groupedDocNodes(c), "Docs", components.TreePanelOpts{
		Border:                 true,
		ToggleBranchesOnSelect: true,
		OnSelect: func(sh *core.Shared, node components.TreeNode) core.Action {
			if item, ok := node.Item.(groupedDocItem); ok {
				return s.pickDoc(sh, item.docItem)
			}
			return core.Action{}
		},
		OnKey: func(sh *core.Shared, k string, node components.TreeNode) (core.Action, bool) {
			if item, ok := node.Item.(groupedDocItem); ok {
				return s.docsKey(sh, k, item.docItem)
			}
			return core.Action{}, false
		},
	})
}
