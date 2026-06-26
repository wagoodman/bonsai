// Package configedit reads and writes the bonsai config's analysis lists (lock, controlled,
// unlock) while preserving the rest of the file — including comments and formatting — by
// manipulating the YAML node tree rather than round-tripping through a struct.
package configedit

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/wagoodman/bonsai/internal"
)

// the config nests the pattern lists under the "analysis" section (matching the CLI config
// struct: analysis.lock / analysis.controlled / analysis.unlock).
const (
	sectionKey    = "analysis"
	lockKey       = "lock"
	controlledKey = "controlled"
	unlockKey     = "unlock"
)

// FindConfig resolves the bonsai config file to read within dir (empty dir means the current
// directory): the first existing default name, else the primary default (".bonsai.yaml" under
// dir) even if absent, so callers always get a path to hand to ReadBuild (which treats a missing
// file as empty). This is the non-clio path resolution used by commands and the MCP server that
// bypass fangs config loading.
func FindConfig(dir string) string {
	if dir == "" {
		dir = "."
	}
	defaults := []string{
		"." + internal.ApplicationName + ".yaml",
		"." + internal.ApplicationName + ".yml",
		internal.ApplicationName + ".yaml",
		internal.ApplicationName + ".yml",
		"." + internal.ApplicationName + "/config.yaml",
	}
	for _, name := range defaults {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(dir, defaults[0])
}

// ReadLock returns the lock patterns currently in the config file at path. A missing
// file or absent lock list yields an empty slice and no error.
func ReadLock(path string) ([]string, error) {
	lock, _, _, err := ReadBuild(path)
	return lock, err
}

// ReadBuild returns the lock, controlled, and unlock pattern lists from the config's analysis
// section. A missing file or any absent key yields an empty slice (no error); explore reads a
// deliberately narrow slice of the config this way, without pulling in the full fangs stack.
func ReadBuild(path string) (lock, controlled, unlock []string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	root := documentRoot(&doc)
	if root == nil {
		return nil, nil, nil, nil
	}
	section := mapValue(root, sectionKey)
	if section == nil {
		return nil, nil, nil, nil
	}
	return readSeq(section, lockKey), readSeq(section, controlledKey), readSeq(section, unlockKey), nil
}

// WriteLock sets the config's lock list to patterns, preserving every other key,
// comment, and the document's existing formatting. The file (and the analysis/lock nodes)
// are created if absent.
func WriteLock(path string, patterns []string) error {
	doc, section, err := openConfigForWrite(path)
	if err != nil {
		return err
	}
	writeSeq(section, lockKey, patterns, true)
	return marshalConfig(path, doc)
}

// WriteBuild sets the config's lock, controlled, and unlock lists, preserving every other key,
// comment, and the document's existing formatting. An empty list whose key is absent is left
// alone (no empty key is added); an empty list whose key already exists is cleared.
func WriteBuild(path string, lock, controlled, unlock []string) error {
	doc, section, err := openConfigForWrite(path)
	if err != nil {
		return err
	}
	writeSeq(section, lockKey, lock, len(lock) > 0)
	writeSeq(section, controlledKey, controlled, len(controlled) > 0)
	writeSeq(section, unlockKey, unlock, len(unlock) > 0)
	return marshalConfig(path, doc)
}

// readSeq returns the string values of the sequence node at key within section, or nil if the
// key is absent or not a sequence.
func readSeq(section *yaml.Node, key string) []string {
	seq := mapValue(section, key)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(seq.Content))
	for _, item := range seq.Content {
		out = append(out, item.Value)
	}
	return out
}

// writeSeq replaces the contents of the sequence at key within section with patterns. The key
// node (and its comments) is untouched if it already exists. When the key is absent it is only
// created if createIfMissing is set, so callers can avoid adding empty keys for lists the user
// never populated.
func writeSeq(section *yaml.Node, key string, patterns []string, createIfMissing bool) {
	seq := mapValue(section, key)
	if seq == nil {
		if !createIfMissing {
			return
		}
		seq = &yaml.Node{Kind: yaml.SequenceNode}
		appendKeyValue(section, key, seq)
	}
	seq.Kind = yaml.SequenceNode
	seq.Tag = "!!seq"
	seq.Content = seq.Content[:0]
	for _, p := range patterns {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: p})
	}
}

// openConfigForWrite loads the document at path (or starts a fresh one if absent), ensuring the
// root mapping and the analysis section exist, and returns the document and section nodes ready
// for writeSeq.
func openConfigForWrite(path string) (doc *yaml.Node, section *yaml.Node, err error) {
	doc = &yaml.Node{}
	if data, e := os.ReadFile(path); e == nil {
		if e := yaml.Unmarshal(data, doc); e != nil {
			return nil, nil, fmt.Errorf("parsing %s: %w", path, e)
		}
	} else if !os.IsNotExist(e) {
		return nil, nil, e
	}

	root := documentRoot(doc)
	if root == nil {
		// fresh document: a single mapping node at the root.
		root = &yaml.Node{Kind: yaml.MappingNode}
		*doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	}

	section = mapValue(root, sectionKey)
	if section == nil {
		section = &yaml.Node{Kind: yaml.MappingNode}
		appendKeyValue(root, sectionKey, section)
	}
	return doc, section, nil
}

// marshalConfig encodes doc back to path.
func marshalConfig(path string, doc *yaml.Node) error {
	out, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	return os.WriteFile(path, out, 0o644) //nolint:gosec // user-editable config file; 0644 is the intended permission
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
