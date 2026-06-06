// Package configedit reads and writes the bonsai config's ignore list while preserving the
// rest of the file — including comments and formatting — by manipulating the YAML node tree
// rather than round-tripping through a struct.
package configedit

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// the config nests the ignore list under the "analysis" section (matching the CLI config
// struct: analysis.ignore).
const (
	sectionKey = "analysis"
	ignoreKey  = "ignore"
)

// ReadIgnore returns the ignore patterns currently in the config file at path. A missing
// file or absent ignore list yields an empty slice and no error.
func ReadIgnore(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	root := documentRoot(&doc)
	if root == nil {
		return nil, nil
	}
	section := mapValue(root, sectionKey)
	if section == nil {
		return nil, nil
	}
	seq := mapValue(section, ignoreKey)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil, nil
	}
	out := make([]string, 0, len(seq.Content))
	for _, item := range seq.Content {
		out = append(out, item.Value)
	}
	return out, nil
}

// WriteIgnore sets the config's ignore list to patterns, preserving every other key,
// comment, and the document's existing formatting. The file (and the analysis/ignore nodes)
// are created if absent.
func WriteIgnore(path string, patterns []string) error {
	var doc yaml.Node
	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	root := documentRoot(&doc)
	if root == nil {
		// fresh document: a single mapping node at the root.
		root = &yaml.Node{Kind: yaml.MappingNode}
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	}

	section := mapValue(root, sectionKey)
	if section == nil {
		section = &yaml.Node{Kind: yaml.MappingNode}
		appendKeyValue(root, sectionKey, section)
	}

	seq := mapValue(section, ignoreKey)
	if seq == nil {
		seq = &yaml.Node{Kind: yaml.SequenceNode}
		appendKeyValue(section, ignoreKey, seq)
	}
	// replace the sequence contents wholesale; the key node (and its comments) is untouched.
	seq.Kind = yaml.SequenceNode
	seq.Tag = "!!seq"
	seq.Content = seq.Content[:0]
	for _, p := range patterns {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: p})
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	return os.WriteFile(path, out, 0o644)
}

// documentRoot returns the mapping node that is the body of a parsed YAML document, or nil
// if the document is empty or not a mapping.
func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	return root
}

// mapValue returns the value node for key in a mapping node, or nil. Mapping content is
// stored as alternating key/value entries.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// appendKeyValue adds a new key/value pair to a mapping node.
func appendKeyValue(m *yaml.Node, key string, value *yaml.Node) {
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}
